package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/invoke"
)

// wire.go is the three formats the vocabulary offers, and nothing else.
//
// # Two shapes, not three
//
// `openai` and `ollama` are the same request and the same answer — which is the
// whole reason the vocabulary's field is called `format` and not `provider`:
// plenty of things that are not OpenAI serve the OpenAI format. `anthropic` is
// the one that differs, in three ways that each break a naive translation:
//
//   - the SYSTEM turn is a top-level field, not a message. Sending it as a
//     message is a 400, not a degraded answer.
//   - `max_tokens` is REQUIRED. There is no provider default to fall back on.
//   - the answer is a list of content BLOCKS, so the text is assembled rather
//     than read out of one field.
//
// # What the resource declares and the caller may not have
//
// Temperature and top_p come from the resource unless the caller set
// temperature. `max_tokens` has already been clamped by the host — see
// pkg/invoke/wasm/model.go — so what arrives here is a number the author's
// ceiling permits. The stop list is the resource's plus the caller's, in that
// order, because an author's stop sequence is a rule and not a default.

// openaiRequest is the shape openai and ollama both take.
type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   *uint32         `json:"max_tokens,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Tools       []openaiTool    `json:"tools,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`

	// Set on an assistant turn the model produced.
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`

	// Set on a `tool` turn, which is the role that format uses for a result.
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type openaiToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function openaiCalledFunc `json:"function"`
}

type openaiCalledFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiTool struct {
	Type     string           `json:"type"`
	Function openaiToolSchema `json:"function"`
}

type openaiToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openaiAnswer struct {
	Choices []struct {
		Message      openaiMessage `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     uint32 `json:"prompt_tokens"`
		CompletionTokens uint32 `json:"completion_tokens"`
	} `json:"usage"`
}

// anthropicRequest keeps the system turn out of the message list.
type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	MaxTokens   uint32             `json:"max_tokens"`
	Stop        []string           `json:"stop_sequences,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

// anthropicMessage carries either plain text or a list of content blocks.
//
// A TEXT-ONLY TURN STAYS A STRING, which is what that API takes and what every
// conversation without tools in it looks like. A turn carrying a tool use or a
// tool result cannot: those ARE blocks and have no string form, which is why
// this field is `any` rather than the shape being wrong.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicToolUse struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type anthropicToolResult struct {
	Type      string `json:"type"`
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

type anthropicAnswer struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  uint32 `json:"input_tokens"`
		OutputTokens uint32 `json:"output_tokens"`
	} `json:"usage"`
}

// anthropicDefaultMaxTokens is what is sent when neither the resource nor the
// caller named one.
//
// IT IS NOT A NUMBER TAU PREFERS. The field is required by that API and has no
// provider default to fall back on, so the alternative is refusing every
// anthropic model whose resource omits an optional field — which would make an
// optional field required for one format and not the others.
const anthropicDefaultMaxTokens = 4096

// providerRequest builds the body for one call.
func providerRequest(format string, grant invoke.Grant, req *invoke.Inference) ([]byte, error) {
	identifier := grant.Settings[specs.ModelKeyIdentifier]
	if identifier == "" {
		return nil, fmt.Errorf("%w: it names no identifier", ErrNoEndpoint)
	}

	temperature := req.Temperature
	if temperature == nil {
		temperature = floatSetting(grant, specs.ModelParametersKeyTemperature)
	}
	topP := floatSetting(grant, specs.ModelParametersKeyTopP)
	stop := append(stopSetting(grant), req.Stop...)

	switch format {
	case specs.ModelFormatAnthropic:
		system, turns := anthropicTurns(req.Messages)
		max := anthropicDefaultMaxTokens
		if req.MaxTokens != nil {
			max = int(*req.MaxTokens)
		}
		return json.Marshal(anthropicRequest{
			Model: identifier, System: system, Messages: turns,
			Temperature: temperature, TopP: topP, MaxTokens: uint32(max), Stop: stop,
			Tools: anthropicTools(req.Tools),
		})

	case specs.ModelFormatOpenai, specs.ModelFormatOllama, "":
		return json.Marshal(openaiRequest{
			Model: identifier, Messages: turnsOf(req.Messages),
			Temperature: temperature, TopP: topP, MaxTokens: req.MaxTokens, Stop: stop,
			Tools: openaiTools(req.Tools),
		})
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownFormat, format)
}

// providerAnswer reads one back.
func providerAnswer(format string, body []byte) (*invoke.Answer, error) {
	switch format {
	case specs.ModelFormatAnthropic:
		var out anthropicAnswer
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("model: that provider's answer is not this format: %w", err)
		}

		// THE TEXT IS ASSEMBLED, because the answer is a list of blocks and a
		// reply with a tool use in it has more than one. The tool uses come out
		// beside it rather than being rendered into the string: a caller needs
		// to CALL them, not read them.
		var text strings.Builder
		var calls []invoke.ToolCall
		for _, block := range out.Content {
			switch block.Type {
			case "text":
				text.WriteString(block.Text)
			case "tool_use":
				calls = append(calls, invoke.ToolCall{
					ID: block.ID, Name: block.Name, Arguments: string(block.Input),
				})
			}
		}
		return &invoke.Answer{
			Content:    text.String(),
			StopReason: out.StopReason,
			ToolCalls:  calls,
			Usage:      invoke.Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens},
		}, nil

	case specs.ModelFormatOpenai, specs.ModelFormatOllama, "":
		var out openaiAnswer
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("model: that provider's answer is not this format: %w", err)
		}
		if len(out.Choices) == 0 {
			return nil, fmt.Errorf("model: that provider answered with no choices")
		}
		var calls []invoke.ToolCall
		for _, c := range out.Choices[0].Message.ToolCalls {
			calls = append(calls, invoke.ToolCall{
				ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments,
			})
		}
		return &invoke.Answer{
			Content:    out.Choices[0].Message.Content,
			StopReason: out.Choices[0].FinishReason,
			ToolCalls:  calls,
			Usage:      invoke.Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens},
		}, nil
	}

	// UNREACHABLE THROUGH Infer and kept anyway: providerRequest refuses a
	// format this node does not speak before any call is made, so nothing can
	// arrive here with one. What it buys is that the two switches are held to
	// the same set — a format added to one and not the other stops being a
	// silently empty answer.
	return nil, fmt.Errorf("%w: %q", ErrUnknownFormat, format)
}

// anthropicTurns lifts the system turn out and turns the tool turns into
// blocks.
//
// EVERY system TURN, joined, and not only the first: a caller that sent two
// meant both, and dropping one would be this node editing a prompt.
//
// A TOOL RESULT IS A **USER** TURN there, which is the one mapping here that is
// not obvious and the one that is a 400 rather than a degraded answer if it is
// got wrong: that API has no `tool` role, and a result sent as one is rejected.
func anthropicTurns(messages []invoke.Message) (string, []anthropicMessage) {
	var system []string
	var out []anthropicMessage

	for _, m := range messages {
		switch {
		case m.Role == "system":
			system = append(system, m.Content)

		case m.Role == "tool":
			out = append(out, anthropicMessage{
				Role: "user",
				Content: []any{anthropicToolResult{
					Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content,
				}},
			})

		case len(m.ToolCalls) > 0:
			blocks := make([]any, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, call := range m.ToolCalls {
				blocks = append(blocks, anthropicToolUse{
					Type: "tool_use", ID: call.ID, Name: call.Name,
					Input: jsonOrEmptyObject(call.Arguments),
				})
			}
			out = append(out, anthropicMessage{Role: m.Role, Content: blocks})

		default:
			out = append(out, anthropicMessage{Role: m.Role, Content: m.Content})
		}
	}
	return strings.Join(system, "\n\n"), out
}

func anthropicTools(tools []invoke.Tool) []anthropicTool {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, anthropicTool{
			Name: t.Name, Description: t.Description, InputSchema: schemaOrObject(t.Schema),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func turnsOf(messages []invoke.Message) []openaiMessage {
	out := make([]openaiMessage, 0, len(messages))
	for _, m := range messages {
		turn := openaiMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, call := range m.ToolCalls {
			turn.ToolCalls = append(turn.ToolCalls, openaiToolCall{
				ID: call.ID, Type: "function",
				Function: openaiCalledFunc{Name: call.Name, Arguments: call.Arguments},
			})
		}
		out = append(out, turn)
	}
	return out
}

func openaiTools(tools []invoke.Tool) []openaiTool {
	out := make([]openaiTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, openaiTool{
			Type: "function",
			Function: openaiToolSchema{
				Name: t.Name, Description: t.Description, Parameters: schemaOrObject(t.Schema),
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// schemaOrObject is the tool's own schema, or the one that says "an object,
// contents unspecified" — which is what a tau function is.
//
// A SCHEMA THAT IS NOT JSON IS REPLACED RATHER THAN SENT. It would be a 400
// from every provider, and the caller's mistake is better answered by a tool
// the model can still call than by a request that fails outright.
func schemaOrObject(schema string) json.RawMessage {
	if schema != "" && json.Valid([]byte(schema)) {
		return json.RawMessage(schema)
	}
	return json.RawMessage(`{"type":"object"}`)
}

// jsonOrEmptyObject keeps a provider from being handed something it will refuse
// to parse. The arguments came from a MODEL, which is not a source that can be
// relied on to produce well-formed JSON every time.
func jsonOrEmptyObject(raw string) json.RawMessage {
	if raw != "" && json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	return json.RawMessage(`{}`)
}

func floatSetting(grant invoke.Grant, key string) *float64 {
	raw, ok := grant.Settings[key]
	if !ok {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}

// stopSetting reads the list back out of the one key it travels in. The
// separator is a newline for the reason modelSettings gives.
func stopSetting(grant invoke.Grant) []string {
	raw := grant.Settings[specs.ModelParametersKeyStop]
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
