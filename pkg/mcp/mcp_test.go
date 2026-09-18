package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/mcp"
)

// mcp_test drives the protocol as a client does: a JSON-RPC message in, a
// JSON-RPC message out. Asserting on the Go types would pass for a server no
// client can talk to.

// registry is a tool set with one behaviour per name.
type registry struct {
	tools []mcp.Tool
	calls map[string]func(json.RawMessage) (mcp.Result, error)

	listErr error
	saw     json.RawMessage
}

func (r *registry) List(context.Context) ([]mcp.Tool, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.tools, nil
}

func (r *registry) Call(_ context.Context, name string, args json.RawMessage) (mcp.Result, error) {
	r.saw = args
	f, ok := r.calls[name]
	if !ok {
		return mcp.Result{}, fmt.Errorf("%w: %s", mcp.ErrNoSuchTool, name)
	}
	return f(args)
}

func serving() *registry {
	return &registry{
		tools: []mcp.Tool{
			{Name: "checkout", Description: "places an order"},
			{Name: "lookup", Description: "finds one", Schema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`)},
		},
		calls: map[string]func(json.RawMessage) (mcp.Result, error){
			"checkout": func(json.RawMessage) (mcp.Result, error) { return mcp.Result{Content: "ok"}, nil },
			"lookup":   func(a json.RawMessage) (mcp.Result, error) { return mcp.Result{Content: string(a)}, nil },
			"broken": func(json.RawMessage) (mcp.Result, error) {
				return mcp.Result{Content: "it went wrong", Failed: true}, nil
			},
			"exploded": func(json.RawMessage) (mcp.Result, error) { return mcp.Result{}, errors.New("nothing could run it") },
		},
	}
}

// ask sends one message and decodes the answer.
func ask(t *testing.T, tools mcp.Tools, message string) map[string]any {
	t.Helper()

	answer, notification, err := mcp.Serve(context.Background(), tools, []byte(message))
	require.NoError(t, err)
	require.False(t, notification, "that message expected an answer")

	var out map[string]any
	require.NoError(t, json.Unmarshal(answer, &out), string(answer))
	require.Equal(t, "2.0", out["jsonrpc"])
	return out
}

// TestINITIALIZEANSWERSWITHWHATTHISSERVERIS, and declares only what it has.
func TestInitializeDeclaresToolsAndNothingElse(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	result := out["result"].(map[string]any)
	require.Equal(t, mcp.Version, result["protocolVersion"],
		"the version this server speaks, not the one it was asked for")

	caps := result["capabilities"].(map[string]any)
	require.Contains(t, caps, "tools")
	require.NotContains(t, caps, "resources", "nothing here can publish one")
	require.NotContains(t, caps, "prompts", "nor one of these")
}

// TestTHEVERSIONISANSWEREDANDNOTECHOED. Echoing what a client asked for would
// claim support for revisions that do not exist.
func TestTheProtocolVersionIsNotEchoed(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)
	require.Equal(t, mcp.Version, out["result"].(map[string]any)["protocolVersion"])
}

// TestANOTIFICATIONISNOTANSWERED, which is the protocol's rule: a message with
// no id expects nothing back.
func TestAMessageWithNoIdGetsNoAnswer(t *testing.T) {
	answer, notification, err := mcp.Serve(context.Background(), serving(),
		[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	require.NoError(t, err)
	require.True(t, notification)
	require.Empty(t, answer)
}

// TestTOOLSLISTISWHATTHESERVERPUBLISHES, with the schema each one declared.
func TestToolsListCarriesNamesDescriptionsAndSchemas(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	tools := out["result"].(map[string]any)["tools"].([]any)
	require.Len(t, tools, 2)

	first := tools[0].(map[string]any)
	require.Equal(t, "checkout", first["name"])
	require.Equal(t, "places an order", first["description"])
	require.Equal(t, map[string]any{"type": "object"}, first["inputSchema"],
		"a function takes a body and cannot say what shape it is — an object with no properties says that")

	second := tools[1].(map[string]any)
	require.Contains(t, second["inputSchema"].(map[string]any), "properties",
		"and a tool that CAN say keeps what it said")
}

// TestCALLINGATOOLPASSESITSARGUMENTSTHROUGH.
func TestCallingAToolHandsItTheArguments(t *testing.T) {
	r := serving()
	out := ask(t, r, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"lookup","arguments":{"id":"42"}}}`)

	result := out["result"].(map[string]any)
	require.Equal(t, false, result["isError"])

	content := result["content"].([]any)[0].(map[string]any)
	require.Equal(t, "text", content["type"])
	require.JSONEq(t, `{"id":"42"}`, content["text"].(string))
}

// TestATOOLTHATFAILEDISASUCCESSFULCALL. Conflating the two makes a tool's own
// failure look like a broken server, and takes away the model's ability to read
// it and try something else.
func TestAToolThatFailedIsAResultAndNotAnError(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"broken","arguments":{}}}`)

	require.NotContains(t, out, "error")
	result := out["result"].(map[string]any)
	require.Equal(t, true, result["isError"])
	require.Equal(t, "it went wrong", result["content"].([]any)[0].(map[string]any)["text"])
}

// TestATOOLTHATDOESNOTEXISTISANERROR, not a failed result: nothing ran, and a
// model told "that failed" would try it again with different arguments.
func TestCallingAToolThatIsNotPublishedIsAnRpcError(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ghost","arguments":{}}}`)

	require.NotContains(t, out, "result")
	require.EqualValues(t, -32602, out["error"].(map[string]any)["code"])
}

// TestACALLTHATCOULDNOTBEMADEATALL is an internal error.
func TestAToolThatCouldNotBeRunIsAnInternalError(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"exploded","arguments":{}}}`)
	require.EqualValues(t, -32603, out["error"].(map[string]any)["code"])
}

// TestAMETHODTHISSERVERDOESNOTIMPLEMENT says so by name.
func TestAnUnknownMethodIsMethodNotFound(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":7,"method":"resources/read","params":{}}`)

	failure := out["error"].(map[string]any)
	require.EqualValues(t, -32601, failure["code"])
	require.Contains(t, failure["message"], "resources/read")
}

// TestGARBAGEISAPARSEERROR, in the protocol's own shape rather than a 500.
func TestSomethingThatIsNotJsonIsAParseError(t *testing.T) {
	answer, _, err := mcp.Serve(context.Background(), serving(), []byte("not json at all"))
	require.NoError(t, err)

	var out map[string]any
	require.NoError(t, json.Unmarshal(answer, &out))
	require.EqualValues(t, -32700, out["error"].(map[string]any)["code"])
}

// TestTHEWRONGPROTOCOLVERSIONISREFUSED. JSON-RPC 2.0 is the only one MCP uses.
func TestAMessageThatIsNotJsonRpc2IsRefused(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"1.0","id":8,"method":"tools/list"}`)
	require.EqualValues(t, -32600, out["error"].(map[string]any)["code"])
}

// TestPINGISANSWERED, because a client uses it to find out whether this server
// is still there.
func TestPingIsAnsweredWithAnEmptyResult(t *testing.T) {
	out := ask(t, serving(), `{"jsonrpc":"2.0","id":9,"method":"ping"}`)
	require.Equal(t, map[string]any{}, out["result"])
}

// TestTHEIDCOMESBACKASITWENTOUT, whatever shape it was: a client matches its
// answer by it, and JSON-RPC allows a string or a number.
func TestTheRequestIdIsEchoedInItsOwnType(t *testing.T) {
	require.EqualValues(t, 9, ask(t, serving(), `{"jsonrpc":"2.0","id":9,"method":"ping"}`)["id"])
	require.Equal(t, "abc", ask(t, serving(), `{"jsonrpc":"2.0","id":"abc","method":"ping"}`)["id"])
}

// TestALISTINGTHATCOULDNOTBEREAD is an error rather than an empty catalogue,
// which a model would read as "this server has no tools".
func TestAListingThatFailedIsNotAnEmptyList(t *testing.T) {
	r := serving()
	r.listErr = errors.New("the project could not be read")

	out := ask(t, r, `{"jsonrpc":"2.0","id":10,"method":"tools/list"}`)
	require.EqualValues(t, -32603, out["error"].(map[string]any)["code"])
}
