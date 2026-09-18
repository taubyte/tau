package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"errors"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// model_test drives a real HTTP provider, because what this backing does is
// speak somebody else's wire format — and a test that asserted on the struct it
// built would pass for a request no provider accepts.

// provider is a fake endpoint that records what it was sent.
type provider struct {
	*httptest.Server

	body   []byte
	header http.Header
	path   string

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

func ask(t *testing.T, g invoke.Grant, req *invoke.Inference) (*invoke.Answer, error) {
	t.Helper()
	return (&inference{}).Infer(context.Background(), g, req)
}

const openaiReply = `{"choices":[{"message":{"role":"assistant","content":"hello there"},"finish_reason":"stop"}],
	"usage":{"prompt_tokens":11,"completion_tokens":3}}`

const anthropicReply = `{"content":[{"type":"text","text":"hello "},{"type":"tool_use"},{"type":"text","text":"there"}],
	"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":3}}`

// TestTHEOPENAIFORMATISWHATAPROVIDERGETS, at the path it serves.
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

// TestOLLAMAISTHEOPENAIFORMAT, which is the whole reason the field is called
// `format` and not `provider`.
func TestAnOllamaModelSpeaksTheSameShape(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "ollama", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Equal(t, "/v1/chat/completions", p.path)
	require.Equal(t, "claude-opus-5", p.sent(t)["model"])
}

// TestANTHROPICDIFFERSINTHREEWAYS, and each of them is a 400 rather than a
// degraded answer if it is got wrong.
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
	require.EqualValues(t, anthropicDefaultMaxTokens, body["max_tokens"],
		"the field is required and has no provider default to fall back on")

	require.Equal(t, "hello there", out.Content, "the text blocks, assembled, with the tool use skipped")
	require.Equal(t, "end_turn", out.StopReason)
	require.Equal(t, uint32(11), out.Usage.InputTokens)
}

// TestTHECREDENTIALGOESWHERETHEFORMATEXPECTS. Getting this wrong is a 401 that
// reads like a bad key.
func TestTheCredentialIsSentTheWayEachFormatWants(t *testing.T) {
	for _, c := range []struct {
		format string
		check  func(*testing.T, http.Header)
	}{
		{"openai", func(t *testing.T, h http.Header) {
			require.Equal(t, "Bearer s3cret", h.Get("authorization"))
			require.Empty(t, h.Get("x-api-key"))
		}},
		{"anthropic", func(t *testing.T, h http.Header) {
			require.Equal(t, "s3cret", h.Get("x-api-key"))
			require.Equal(t, anthropicVersion, h.Get("anthropic-version"), "the header is required")
			require.Empty(t, h.Get("authorization"))
		}},
	} {
		t.Run(c.format, func(t *testing.T) {
			reply := openaiReply
			if c.format == "anthropic" {
				reply = anthropicReply
			}
			p := newProvider(t, reply)

			_, err := (&inference{secrets: &fixedVault{value: "s3cret"}}).Infer(context.Background(),
				modelGrant(p.URL, c.format, map[string]string{"inference/remote/credentials/name": "QmKey"}),
				&invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})
			require.NoError(t, err)

			c.check(t, p.header)
		})
	}
}

// TestNOCREDENTIALISNOHEADER. A model served by something on this machine needs
// none, and refusing it would make the credential decide whether inference
// exists at all.
func TestAModelWithNoCredentialSendsNoAuthorization(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "openai", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.NoError(t, err)
	require.Empty(t, p.header.Get("authorization"))
}

// TestWHATTHERESOURCEDECLARESISSENT when the caller said nothing.
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

// TestTHECALLERADDSTOTHESTOPLISTRATHERTHANREPLACINGIT: an author's stop
// sequence is a rule about what this model may emit.
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

// TestAPROVIDERTHATSAIDNOISREFUSED, and one that could not answer is not: a
// caller retries the second and wastes its budget on the first.
func TestA4xxIsRefusedAndA5xxIsNot(t *testing.T) {
	for _, c := range []struct {
		status  int
		refused bool
	}{
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

// TestAMODELWITHNOWHERETOSEND is named rather than dialled.
func TestAModelWithNoEndpointIsRefusedByName(t *testing.T) {
	_, err := ask(t, invoke.Grant{
		Kind: "model", Label: "brain", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
		Settings: map[string]string{"identifier": "m", "format": "openai", "inference/kind": "remote"},
	}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorIs(t, err, ErrNoModelEndpoint)
}

// TestALOCALMODELISNOTBUILT, and says so rather than dialling something else.
func TestALocallyServedModelIsNamedAsMissing(t *testing.T) {
	_, err := ask(t, invoke.Grant{
		Kind: "model", Label: "brain", Handle: "shop/prod/QmModel", Ops: []string{"infer"},
		Settings: map[string]string{"identifier": "m", "format": "openai", "inference/kind": "local",
			"inference/local/name": "llama"},
	}, &invoke.Inference{Messages: []invoke.Message{{Role: "user", Content: "hi"}}})

	require.ErrorIs(t, err, ErrNoModelEndpoint)
	require.ErrorContains(t, err, "served by a workload")
}

// TestAFORMATTHISNODEDOESNOTSPEAK is refused rather than guessed at.
func TestAnUnknownFormatIsRefused(t *testing.T) {
	p := newProvider(t, openaiReply)

	_, err := ask(t, modelGrant(p.URL, "cohere", nil), &invoke.Inference{
		Messages: []invoke.Message{{Role: "user", Content: "hi"}},
	})
	require.ErrorIs(t, err, ErrUnknownModelFormat)
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }

// fixedVault answers every read with one value.
type fixedVault struct{ value string }

func (v *fixedVault) Secret(context.Context, string, string, string) (string, error) {
	return v.value, nil
}
