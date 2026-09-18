package node

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
)

// model_wire.go is the three formats the vocabulary offers, and nothing else.
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
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
	Model       string          `json:"model"`
	System      string          `json:"system,omitempty"`
	Messages    []openaiMessage `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	MaxTokens   uint32          `json:"max_tokens"`
	Stop        []string        `json:"stop_sequences,omitempty"`
}

type anthropicAnswer struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
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
	identifier := grant.Settings["identifier"]
	if identifier == "" {
		return nil, fmt.Errorf("%w: it names no identifier", ErrNoModelEndpoint)
	}

	temperature := req.Temperature
	if temperature == nil {
		temperature = floatSetting(grant, "parameters/temperature")
	}
	topP := floatSetting(grant, "parameters/top_p")
	stop := append(stopSetting(grant), req.Stop...)

	switch format {
	case "anthropic":
		system, turns := splitSystem(req.Messages)
		max := anthropicDefaultMaxTokens
		if req.MaxTokens != nil {
			max = int(*req.MaxTokens)
		}
		return json.Marshal(anthropicRequest{
			Model: identifier, System: system, Messages: turns,
			Temperature: temperature, TopP: topP, MaxTokens: uint32(max), Stop: stop,
		})

	case "openai", "ollama", "":
		return json.Marshal(openaiRequest{
			Model: identifier, Messages: turnsOf(req.Messages),
			Temperature: temperature, TopP: topP, MaxTokens: req.MaxTokens, Stop: stop,
		})
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownModelFormat, format)
}

// providerAnswer reads one back.
func providerAnswer(format string, body []byte) (*invoke.Answer, error) {
	switch format {
	case "anthropic":
		var out anthropicAnswer
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("node: that provider's answer is not this format: %w", err)
		}

		// THE TEXT IS ASSEMBLED, because the answer is a list of blocks and a
		// reply with a tool use in it has more than one. Non-text blocks are
		// skipped rather than rendered: this interface answers with a string.
		var text strings.Builder
		for _, block := range out.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		return &invoke.Answer{
			Content:    text.String(),
			StopReason: out.StopReason,
			Usage:      invoke.Usage{InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens},
		}, nil

	case "openai", "ollama", "":
		var out openaiAnswer
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("node: that provider's answer is not this format: %w", err)
		}
		if len(out.Choices) == 0 {
			return nil, fmt.Errorf("node: that provider answered with no choices")
		}
		return &invoke.Answer{
			Content:    out.Choices[0].Message.Content,
			StopReason: out.Choices[0].FinishReason,
			Usage:      invoke.Usage{InputTokens: out.Usage.PromptTokens, OutputTokens: out.Usage.CompletionTokens},
		}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnknownModelFormat, format)
}

// splitSystem lifts the system turn out, which is where anthropic wants it.
//
// EVERY system TURN, joined, and not only the first: a caller that sent two
// meant both, and dropping one would be this node editing a prompt.
func splitSystem(messages []invoke.Message) (string, []openaiMessage) {
	var system []string
	var turns []openaiMessage

	for _, m := range messages {
		if m.Role == "system" {
			system = append(system, m.Content)
			continue
		}
		turns = append(turns, openaiMessage{Role: m.Role, Content: m.Content})
	}
	return strings.Join(system, "\n\n"), turns
}

func turnsOf(messages []invoke.Message) []openaiMessage {
	out := make([]openaiMessage, 0, len(messages))
	for _, m := range messages {
		out = append(out, openaiMessage{Role: m.Role, Content: m.Content})
	}
	return out
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
	raw := grant.Settings["parameters/stop"]
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\n")
}
