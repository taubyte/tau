package model_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"errors"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/model"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
)

// Drives a real HTTP provider, since asserting on the built struct would pass for a
// request no provider accepts.

// provider is a fake endpoint that records what it was sent.
type provider struct {
	*httptest.Server

	body   []byte
	header http.Header
	path   string
	host   string

	status int
	answer string
}

func newProvider(t *testing.T, answer string) *provider {
	t.Helper()

	p := &provider{status: 200, answer: answer}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.body, _ = readAll(r)
		p.header = r.Header.Clone()
		p.path = r.URL.Path
		p.host = r.Host

		w.Header().Set("content-type", "application/json")
		w.WriteHeader(p.status)
		w.Write([]byte(p.answer))
	}))
	t.Cleanup(p.Close)
	return p
}

func readAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

// sent is what the provider was handed, as a map.
func (p *provider) sent(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(p.body, &out))
	return out
}

func modelGrant(endpoint, format string, extra map[string]string) invoke.Grant {
	settings := map[string]string{
		"identifier":                "claude-opus-5",
		"format":                    format,
		"inference/kind":            "remote",
		"inference/remote/endpoint": endpoint,
	}
	for k, v := range extra {
		settings[k] = v
	}
	return invoke.Grant{
		Kind: "model", Label: "brain", Handle: "shop/prod/QmModel",
		Ops: []string{"infer"}, Settings: settings,
	}
}

// asking is an inference over whichever of the two collaborators a case has.
// Both functions are always given, because the composition always has the
// question to ask — nil is the ANSWER on a node with no vault or no placement.
func asking(t *testing.T, vault model.Secrets, where model.Workers) *model.Inferences {
	t.Helper()

	i, err := model.New(
		func() model.Secrets { return vault },
		func() model.Workers { return where })
	require.NoError(t, err)
	return i
}

func ask(t *testing.T, g invoke.Grant, req *invoke.Inference) (*invoke.Answer, error) {
	t.Helper()
	return asking(t, nil, nil).Infer(context.Background(), g, req)
}

func TestTheAdapterNeedsBothQuestions(t *testing.T) {
	_, err := model.New(nil, func() model.Workers { return nil })
	require.ErrorContains(t, err, "credential")

	_, err = model.New(func() model.Secrets { return nil }, nil)
	require.ErrorContains(t, err, "workload")
}

const openaiReply = `{"choices":[{"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":11,"completion_tokens":3}}`

const anthropicReply = `{"content":[{"type":"text","text":"hello "},{"type":"tool_use"},{"type":"text","text":"there"}],
	"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":3}}`

func TestAnOpenAiModelIsCalledInItsOwnFormat(t *testing.T) {
	p := newProvider(t, openaiReply)

	out, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)

	require.Equal(t, "/v1/chat/completions", p.path)
	body := p.sent(t)
	require.Equal(t, "claude-opus-5", body["model"], "the identifier is theirs, not the resource's name")
	require.Len(t, body["messages"], 1)

	require.Equal(t, "hello there", out.Content)
	require.Equal(t, "stop", out.StopReason)
	require.Equal(t, uint32(11), out.Usage.InputTokens)
	require.Equal(t, uint32(3), out.Usage.OutputTokens)
}

func TestAnOllamaModelSpeaksTheSameShape(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "ollama", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Equal(t, "/v1/chat/completions", p.path)
	require.Equal(t, "claude-opus-5", p.sent(t)["model"])
}

func TestAnAnthropicModelGetsItsOwnShape(t *testing.T) {
	p := newProvider(t, anthropicReply)

	out, err := ask(t, modelGrant(p.URL, "anthropic", nil), &invoke.Inference{
		Messages: []invoke.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hi"},
			{Role: "system", Content: "and polite"},
		},
	})
	require.NoError(t, err)

	require.Equal(t, "/v1/messages", p.path)

	body := p.sent(t)
	require.Equal(t, "be brief\n\nand polite", body["system"],
		"every system turn, lifted out and joined — dropping one would be editing a prompt")
	require.Len(t, body["messages"], 1, "and none of them left in the message list")
	require.EqualValues(t, 4096, body["max_tokens"],
		"the field is required and has no provider default to fall back on")

	require.Equal(t, "hello there", out.Content, "the text blocks, assembled, with the tool use skipped")
	require.Equal(t, "end_turn", out.StopReason)
	require.Equal(t, uint32(11), out.Usage.InputTokens)
}

func TestTheCredentialIsSentTheWayEachFormatWants(t *testing.T) {
	type testCase struct {
		format string
		check  func(*testing.T, http.Header)
	}
	for _, c := range []testCase{
		{"openai", func(t *testing.T, h http.Header) {
			require.Equal(t, "Bearer s3cret", h.Get("authorization"))
			require.Empty(t, h.Get("x-api-key"))
		}},
		{"anthropic", func(t *testing.T, h http.Header) {
			require.Equal(t, "s3cret", h.Get("x-api-key"))
			require.Equal(t, "2023-06-01", h.Get("anthropic-version"), "the header is required")
			require.Empty(t, h.Get("authorization"))
		}},
	} {
		t.Run(c.format, func(t *testing.T) {
			reply := openaiReply
			if c.format == "anthropic" {
				reply = anthropicReply
			}
			p := newProvider(t, reply)

			held := &fixedVault{value: "s3cret"}
			_, err := asking(t, held, nil).Infer(context.Background(),
				modelGrant(p.URL, c.format, map[string]string{"inference/remote/credentials/name": "QmKey"}),
				&invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
			require.NoError(t, err)

			c.check(t, p.header)
		})
	}
}

func TestAModelWithNoCredentialSendsNoAuthorization(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Empty(t, p.header.Get("authorization"))
}

func TestTheResourcesOwnParametersReachTheProvider(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", map[string]string{
		"parameters/temperature": "0.2",
		"parameters/top_p":       "0.9",
		"parameters/stop":        "END\nHALT",
	}), &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)

	body := p.sent(t)
	require.InDelta(t, 0.2, body["temperature"], 0.0001)
	require.InDelta(t, 0.9, body["top_p"], 0.0001)
	require.Equal(t, []any{"END", "HALT"}, body["stop"], "a list, back out of the one key it travels in")
}

func TestACallersStopSequencesAreAddedToTheResources(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", map[string]string{"parameters/stop": "END"}),
		&invoke.Inference{
			Messages: []invoke.Message{{Role: "user", Content: "hi"}},
			Stop:     []string{"MINE"},
		})
	require.NoError(t, err)
	require.Equal(t, []any{"END", "MINE"}, p.sent(t)["stop"])
}

func TestA4xxIsRefusedAndA5xxIsNot(t *testing.T) {
	type testCase struct {
		status  int
		refused bool
	}
	for _, c := range []testCase{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusUnprocessableEntity, true},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
	} {
		p := newProvider(t, `{"error":"nope"}`)
		p.status = c.status

		_, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
			Messages: []invoke.Message{{Role: "user", Content: "hi"}},
		})
		require.Error(t, err)
		require.Equal(t, c.refused, errorsIs(err, wasmtier.ErrRefusedByProvider),
			"status %d", c.status)
	}
}

func TestAModelWithNoEndpointIsRefusedByName(t *testing.T) {
	_, err := ask(t, invoke.Grant{
		Kind: "model", Label: "brain", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
		Settings: map[string]string{"identifier": "m", "format": "openai", "inference/kind": "remote"},
	}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorIs(t, err, model.ErrNoEndpoint)
}

func TestALocallyServedModelIsNamedAsMissing(t *testing.T) {
	_, err := ask(t, invoke.Grant{
		Kind: "model", Label: "brain", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
		Settings: map[string]string{"identifier": "m", "format": "openai", "inference/kind": "local",
			"inference/local/name": "llama"},
	}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorIs(t, err, model.ErrNoEndpoint)
	require.ErrorContains(t, err, "served by a workload")
}

func TestAnUnknownFormatIsRefused(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "cohere", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.ErrorIs(t, err, model.ErrUnknownFormat)
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }

// fixedVault answers every read with one value.
type fixedVault struct{ value string }

func (v *fixedVault) Secret(context.Context, string, string, string) (string, error) {
	return v.value, nil
}

// dialled is a placement whose Conn goes to one address, recording what it was
// asked for.
type dialled struct {
	at   specs.Address
	port string
	to   string
}

func (d *dialled) Conn(ctx context.Context, at specs.Address, port string) (net.Conn, error) {
	d.at, d.port = at, port
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.to)
}

func TestALocalModelIsDialledThroughPlacement(t *testing.T) {
	p := newProvider(t, openaiReply)
	where := &dialled{to: strings.TrimPrefix(p.URL, "http://")}

	grant := invoke.Grant{
		Kind: "model", Label: "brain", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
		Settings: map[string]string{
			"identifier":            "llama-3.1-70b",
			"format":                "openai",
			"inference/kind":        "local",
			"inference/local/name":  "llama",
			"inference/local/port":  "http",
			"inference/local/scope": "local/ml",
		},
	}

	out, err := asking(t, nil, where).
		Infer(context.Background(), grant,
			&invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)
	require.Equal(t, "hello there", out.Content)

	// It asked placement for the right workload, on the right port, in the
	// namespace the composer resolved.
	require.Equal(t, "application", where.at.Kind)
	require.Equal(t, "llama", where.at.Name)
	require.Equal(t, "shop", where.at.Project)
	require.Equal(t, "prod", where.at.Environment)
	require.Equal(t, []string{"local", "ml"}, where.at.Scope)
	require.Equal(t, "http", where.port)

	// And it spoke the format, at the path, exactly as a remote one would.
	require.Equal(t, "/v1/chat/completions", p.path)
	require.Equal(t, "llama-3.1-70b", p.sent(t)["model"])
}

func TestALocalModelsUrlIsNotAResolvableName(t *testing.T) {
	p := newProvider(t, openaiReply)
	where := &dialled{to: strings.TrimPrefix(p.URL, "http://")}

	_, err := asking(t, nil, where).
		Infer(context.Background(), invoke.Grant{
			Kind: "model", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
			Settings: map[string]string{
				"identifier": "m", "format": "openai",
				"inference/kind": "local", "inference/local/name": "llama",
			},
		}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)

	// The Host header is what the URL said, and nothing resolved it: the
	// connection came from the dialler above.
	require.Equal(t, "model.invalid", p.host)
}

func TestALocalModelWithNoPlacementIsRefusedByName(t *testing.T) {
	_, err := ask(t, invoke.Grant{
		Kind: "model", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
		Settings: map[string]string{"identifier": "m", "format": "openai",
			"inference/kind": "local", "inference/local/name": "llama"},
	}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorIs(t, err, model.ErrNoEndpoint)
	require.ErrorContains(t, err, "reaches none")
}

func TestALocalModelWithNoWorkloadIsRefused(t *testing.T) {
	_, err := asking(t, nil, &dialled{}).
		Infer(context.Background(), invoke.Grant{
			Kind: "model", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
			Settings: map[string]string{"identifier": "m", "format": "openai", "inference/kind": "local"},
		}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorIs(t, err, model.ErrNoEndpoint)
	require.ErrorContains(t, err, "names no workload")
}

const openaiToolReply = `{"choices":[{"message":{"role":"assistant","content":"looking",
	"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"42\"}"}}]},
	"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":3}}`

const anthropicToolReply = `{"content":[{"type":"text","text":"looking"},
	{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"id":"42"}}],
	"stop_reason":"tool_use","usage":{"input_tokens":11,"output_tokens":3}}`

func oneTool() []invoke.Tool {
	return []invoke.Tool{{
		Name: "lookup", Description: "finds one",
		Schema: `{"type":"object","properties":{"id":{"type":"string"}}}`,
	}}
}

func TestAnOpenAiModelIsSentToolsAndAnswersWithCalls(t *testing.T) {
	p := newProvider(t, openaiToolReply)

	out, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "find 42"}},
		Tools:    oneTool(),
	})
	require.NoError(t, err)

	tools := p.sent(t)["tools"].([]any)
	require.Len(t, tools, 1)
	first := tools[0].(map[string]any)
	require.Equal(t, "function", first["type"], "that format wraps every tool in a type")
	fn := first["function"].(map[string]any)
	require.Equal(t, "lookup", fn["name"])
	require.Contains(t, fn["parameters"].(map[string]any), "properties")

	require.Equal(t, "looking", out.Content)
	require.Equal(t, "tool_calls", out.StopReason)
	require.Len(t, out.ToolCalls, 1)
	require.Equal(t, "call_1", out.ToolCalls[0].ID)
	require.Equal(t, "lookup", out.ToolCalls[0].Name)
	require.JSONEq(t, `{"id":"42"}`, out.ToolCalls[0].Arguments)
}

func TestAnAnthropicModelIsSentToolsInItsOwnShape(t *testing.T) {
	p := newProvider(t, anthropicToolReply)

	out, err := ask(t, modelGrant(p.URL, "anthropic", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "find 42"}},
		Tools:    oneTool(),
	})
	require.NoError(t, err)

	tools := p.sent(t)["tools"].([]any)
	require.Len(t, tools, 1)
	first := tools[0].(map[string]any)
	require.Equal(t, "lookup", first["name"], "no wrapper: the tool is the object")
	require.Contains(t, first, "input_schema", "and the schema is called something else")
	require.NotContains(t, first, "parameters")

	require.Equal(t, "looking", out.Content, "the text blocks, with the tool use left out of the prose")
	require.Len(t, out.ToolCalls, 1)
	require.Equal(t, "toolu_1", out.ToolCalls[0].ID)
	require.JSONEq(t, `{"id":"42"}`, out.ToolCalls[0].Arguments)
}

func TestAToolResultIsSentAsAUserTurnWithBlocks(t *testing.T) {
	p := newProvider(t, anthropicReply)

	_, err := ask(t, modelGrant(p.URL, "anthropic", nil), &invoke.Inference{
		Messages: []invoke.Message{
			{Role: "user", Content: "find 42"},
			{Role: "assistant", Content: "looking", ToolCalls: []invoke.ToolCall{
				{ID: "toolu_1", Name: "lookup", Arguments: `{"id":"42"}`},
			}},
			{Role: "tool", ToolCallID: "toolu_1", Content: "found it"},
		},
	})
	require.NoError(t, err)

	messages := p.sent(t)["messages"].([]any)
	require.Len(t, messages, 3)

	// The assistant turn became blocks, text first then the tool use.
	assistant := messages[1].(map[string]any)
	blocks := assistant["content"].([]any)
	require.Len(t, blocks, 2)
	require.Equal(t, "text", blocks[0].(map[string]any)["type"])
	use := blocks[1].(map[string]any)
	require.Equal(t, "tool_use", use["type"])
	require.Equal(t, "toolu_1", use["id"])
	require.Equal(t, map[string]any{"id": "42"}, use["input"], "the arguments, as JSON and not as a string")

	// And the result is a USER turn carrying a tool_result block.
	result := messages[2].(map[string]any)
	require.Equal(t, "user", result["role"], "there is no tool role in that API")
	block := result["content"].([]any)[0].(map[string]any)
	require.Equal(t, "tool_result", block["type"])
	require.Equal(t, "toolu_1", block["tool_use_id"])
	require.Equal(t, "found it", block["content"])
}

func TestAToolResultIsSentAsAToolTurnForOpenAi(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{
			{Role: "assistant", ToolCalls: []invoke.ToolCall{
				{ID: "call_1", Name: "lookup", Arguments: `{"id":"42"}`},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "found it"},
		},
	})
	require.NoError(t, err)

	messages := p.sent(t)["messages"].([]any)
	assistant := messages[0].(map[string]any)
	calls := assistant["tool_calls"].([]any)
	require.Len(t, calls, 1)
	require.Equal(t, `{"id":"42"}`, calls[0].(map[string]any)["function"].(map[string]any)["arguments"],
		"that format carries the arguments as a STRING, not as an object")

	result := messages[1].(map[string]any)
	require.Equal(t, "tool", result["role"])
	require.Equal(t, "call_1", result["tool_call_id"])
}

func TestMalformedArgumentsBecomeAnEmptyObject(t *testing.T) {
	p := newProvider(t, anthropicReply)

	_, err := ask(t, modelGrant(p.URL, "anthropic", nil), &invoke.Inference{
		Messages: []invoke.Message{
			{Role: "assistant", ToolCalls: []invoke.ToolCall{
				{ID: "toolu_1", Name: "lookup", Arguments: `{"id": tru`},
			}},
		},
	})
	require.NoError(t, err)

	blocks := p.sent(t)["messages"].([]any)[0].(map[string]any)["content"].([]any)
	require.Equal(t, map[string]any{}, blocks[0].(map[string]any)["input"])
}

func TestAnUnparseableSchemaBecomesAnObject(t *testing.T) {
	for _, format := range []string{"openai", "anthropic"} {
		reply := openaiReply
		if format == "anthropic" {
			reply = anthropicReply
		}
		p := newProvider(t, reply)

		_, err := ask(t, modelGrant(p.URL, format, nil), &invoke.Inference{
			Messages: []invoke.Message{{Role: "user", Content: "hi"}},
			Tools:    []invoke.Tool{{Name: "lookup", Schema: "not a schema"}},
		})
		require.NoError(t, err)

		tools := p.sent(t)["tools"].([]any)
		first := tools[0].(map[string]any)

		schema := first["input_schema"]
		if format == "openai" {
			schema = first["function"].(map[string]any)["parameters"]
		}
		require.Equal(t, map[string]any{"type": "object"}, schema, format)
	}
}

func TestACompletionWithNoToolsSendsNoToolsField(t *testing.T) {
	for _, format := range []string{"openai", "anthropic"} {
		reply := openaiReply
		if format == "anthropic" {
			reply = anthropicReply
		}
		p := newProvider(t, reply)

		_, err := ask(t, modelGrant(p.URL, format, nil), &invoke.Inference{
			Messages: []invoke.Message{{Role: "user", Content: "hi"}},
		})
		require.NoError(t, err)
		require.NotContains(t, p.sent(t), "tools", format)
	}
}

func TestAnEndpointThatIsNotAUrlIsRefused(t *testing.T) {
	_, err := ask(t, modelGrant("http://a b", "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, wasmtier.ErrRefusedByProvider, "nobody answered, so nobody refused")
}

func TestAProviderThatCannotBeReachedIsNotARefusal(t *testing.T) {
	p := newProvider(t, openaiReply)
	endpoint := p.URL
	p.Close()

	_, err := ask(t, modelGrant(endpoint, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, endpoint)
	require.NotErrorIs(t, err, wasmtier.ErrRefusedByProvider)
}

func TestAnAnswerThatStopsHalfWayIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.Header().Set("content-length", "4096")
		w.Write([]byte(`{"choices":[`))
	}))
	t.Cleanup(srv.Close)

	_, err := ask(t, modelGrant(srv.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, srv.URL)
}

func TestAHandleThatNamesNoDeploymentIsRefused(t *testing.T) {
	p := newProvider(t, openaiReply)

	for _, handle := range []string{"", "shop", "shop/prod"} {
		credentialed := modelGrant(p.URL, "openai", map[string]string{
			"inference/remote/credentials/name": "QmKey",
		})
		credentialed.Handle = handle

		_, err := asking(t, &fixedVault{value: "s3cret"}, nil).
			Infer(context.Background(), credentialed,
				&invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
		require.ErrorContains(t, err, "does not name a deployment", "handle %q", handle)

		local := invoke.Grant{
			Kind: "model", Handle: handle, Ops: []string{"infer"},
			Settings: map[string]string{"identifier": "m", "format": "openai",
				"inference/kind": "local", "inference/local/name": "llama"},
		}
		_, err = asking(t, nil, &dialled{}).Infer(context.Background(), local,
			&invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
		require.ErrorContains(t, err, "does not name a deployment", "handle %q", handle)
	}
}

func TestAModelThatDeclaresNoKindIsRefused(t *testing.T) {
	for _, kind := range []string{"", "somewhere-else"} {
		_, err := ask(t, invoke.Grant{
			Kind: "model", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
			Settings: map[string]string{"identifier": "m", "format": "openai", "inference/kind": kind},
		}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
		require.ErrorIs(t, err, model.ErrNoEndpoint, "kind %q", kind)
	}
}

func TestAModelWithNoIdentifierIsRefused(t *testing.T) {
	p := newProvider(t, openaiReply)

	grant := modelGrant(p.URL, "openai", nil)
	delete(grant.Settings, "identifier")

	_, err := ask(t, grant, &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.ErrorIs(t, err, model.ErrNoEndpoint)
	require.ErrorContains(t, err, "identifier")
}

func TestACredentialOnANodeWithNoVaultIsRefused(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", map[string]string{
		"inference/remote/credentials/name": "QmKey",
	}), &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
	require.ErrorContains(t, err, "reads no secret")
}

func TestACredentialThatCannotBeReadIsReported(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := asking(t, &failingVault{err: errors.New("the vault is sealed")}, nil).
		Infer(context.Background(), modelGrant(p.URL, "openai", map[string]string{
			"inference/remote/credentials/name": "QmKey",
		}), &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorContains(t, err, "the vault is sealed")
	require.ErrorContains(t, err, "credential")
}

// failingVault is a resolver that cannot answer.
type failingVault struct{ err error }

func (v *failingVault) Secret(context.Context, string, string, string) (string, error) {
	return "", v.err
}

func TestAnAnswerInTheWrongShapeIsReported(t *testing.T) {
	for _, format := range []string{"openai", "anthropic"} {
		p := newProvider(t, `not json at all`)

		_, err := ask(t, modelGrant(p.URL, format, nil), &invoke.Inference{
			Messages: []invoke.Message{{Role: "user", Content: "hi"}},
		})
		require.ErrorContains(t, err, "not this format", format)
	}
}

func TestAnAnswerWithNoChoicesIsReported(t *testing.T) {
	p := newProvider(t, `{"usage":{"prompt_tokens":1}}`)

	_, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.ErrorContains(t, err, "no choices")
}

func TestALongRefusalIsTruncated(t *testing.T) {
	p := newProvider(t, strings.Repeat("x", 4096))
	p.status = http.StatusBadRequest

	_, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.ErrorIs(t, err, wasmtier.ErrRefusedByProvider)
	require.Less(t, len(err.Error()), 700, "the provider's detail is cut at 512")
}

func TestACallersMaxTokensReplacesTheDefault(t *testing.T) {
	p := newProvider(t, anthropicReply)

	want := uint32(7)
	_, err := ask(t, modelGrant(p.URL, "anthropic", nil), &invoke.Inference{
		Messages:  []invoke.Message{{Role: "user", Content: "hi"}},
		MaxTokens: &want,
	})
	require.NoError(t, err)
	require.EqualValues(t, 7, p.sent(t)["max_tokens"])
}

func TestAnUnparseableParameterIsLeftOut(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", map[string]string{
		"parameters/temperature": "warm",
		"parameters/top_p":       "most of it",
	}), &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
	require.NoError(t, err)

	body := p.sent(t)
	require.NotContains(t, body, "temperature")
	require.NotContains(t, body, "top_p")
}
