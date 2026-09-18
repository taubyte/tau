// Package mcp serves the Model Context Protocol over one request and one
// answer.
//
// # What it is for here
//
// The vocabulary's `mcp` kind is "an MCP server grouping functions as tools",
// and what it groups is what it declares access to: an mcp that may `invoke`
// three functions publishes three tools. There is no second list — the same
// rule an agent's tools follow, and for the same reason: a separate `tools:`
// block would be a grant path the authorizer on the target never sees.
//
// # Stateless, which the protocol allows and this kind requires
//
// Streamable HTTP has two shapes: a POST that answers with JSON, and one that
// upgrades to a stream for server-initiated messages. This serves the first.
// The kind's own doc says "stateless Streamable HTTP; subscriptions are
// declined in v1", so there is no session to keep, no `Mcp-Session-Id` to mint,
// and nothing that outlives the request — which is what lets any node answer
// any call.
//
// # It speaks JSON-RPC and knows nothing about tau
//
// What a tool IS and how one runs are behind Tools. This package parses, routes
// and answers; whoever implements Tools decides that calling one is invoking a
// function on whichever node is warm for it.
//
// # Errors are the protocol's, in the place the protocol puts them
//
// A method that does not exist is a JSON-RPC error. A TOOL that failed is NOT:
// it is a successful result carrying `isError`, because the call was made and
// what came back is what the model has to read. Conflating the two makes a
// tool's own failure look like a broken server and takes the agent's ability to
// react to it away.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the protocol revision this server implements.
//
// IT IS ANSWERED RATHER THAN NEGOTIATED. A client asks for one; a server that
// speaks a different one says which it speaks, and the client decides. Echoing
// whatever was asked for would claim support for revisions that do not exist.
const Version = "2025-06-18"

// Tool is one thing a client may call.
type Tool struct {
	Name        string
	Description string

	// Schema is JSON Schema for the arguments, as raw JSON. Empty is an object
	// with no declared properties — which is what a tau function is: it takes a
	// body, and this vocabulary has no way for it to say what shape that body
	// has.
	Schema json.RawMessage
}

// Result is what a tool answered.
type Result struct {
	// Content is what the model reads.
	Content string

	// Failed says the TOOL failed, which is not the same as the call failing.
	// It reaches the client as a successful result with `isError` set, so an
	// agent can read the message and try something else.
	Failed bool
}

// Tools is what this server publishes and what running one does.
type Tools interface {
	List(ctx context.Context) ([]Tool, error)
	Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error)
}

// ErrNoSuchTool is a call for something this server does not publish.
var ErrNoSuchTool = errors.New("mcp: no such tool")

// JSON-RPC error codes. The first four are the specification's; -32002 is the
// one MCP adds for a resource that is not there.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve answers one POSTed message.
//
// IT RETURNS NO BYTES FOR A NOTIFICATION, which is the protocol's rule and not
// an optimisation: a message with no id expects no answer, and HTTP's way of
// saying that is 202 with an empty body.
func Serve(ctx context.Context, tools Tools, body []byte) (answer []byte, notification bool, err error) {
	var req request
	if unmarshalErr := json.Unmarshal(body, &req); unmarshalErr != nil {
		return reply(nil, nil, &rpcError{Code: codeParse, Message: "that is not JSON"}), false, nil
	}
	if req.JSONRPC != "2.0" {
		return reply(req.ID, nil, &rpcError{Code: codeInvalidRequest, Message: "jsonrpc must be \"2.0\""}), false, nil
	}

	// A NOTIFICATION HAS NO id. `notifications/initialized` is the one every
	// client sends, and answering it is a protocol error rather than noise.
	if len(req.ID) == 0 {
		return nil, true, nil
	}

	switch req.Method {
	case "initialize":
		return reply(req.ID, initialized(), nil), false, nil

	case "ping":
		// Answered with an empty object, which is what the protocol asks for.
		return reply(req.ID, struct{}{}, nil), false, nil

	case "tools/list":
		listed, listErr := tools.List(ctx)
		if listErr != nil {
			return reply(req.ID, nil, &rpcError{Code: codeInternal, Message: listErr.Error()}), false, nil
		}
		return reply(req.ID, listing(listed), nil), false, nil

	case "tools/call":
		return callTool(ctx, tools, req), false, nil
	}

	return reply(req.ID, nil, &rpcError{
		Code:    codeMethodNotFound,
		Message: fmt.Sprintf("this server does not implement %q", req.Method),
	}), false, nil
}

// initialized is what this server says it is and what it can do.
func initialized() map[string]any {
	return map[string]any{
		"protocolVersion": Version,
		"serverInfo":      map[string]any{"name": "tau", "version": "v2"},
		// TOOLS AND NOTHING ELSE. Declaring `resources` or `prompts` would
		// promise two methods this server answers with "not implemented" — and
		// the vocabulary has no field for either, so there is nothing an author
		// could publish through them.
		"capabilities": map[string]any{
			// `listChanged` is false: a stateless server has nobody to notify.
			"tools": map[string]any{"listChanged": false},
		},
	}
}

func listing(tools []Tool) map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema
		if len(schema) == 0 {
			// A TAU FUNCTION TAKES A BODY and this vocabulary has no way for it
			// to say what shape that body has. An object with no declared
			// properties says exactly that, rather than claiming it takes
			// nothing.
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return map[string]any{"tools": out}
}

func callTool(ctx context.Context, tools Tools, req request) []byte {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		return reply(req.ID, nil, &rpcError{Code: codeInvalidParams, Message: "a call needs a tool name"})
	}

	out, err := tools.Call(ctx, params.Name, params.Arguments)
	switch {
	case errors.Is(err, ErrNoSuchTool):
		// NOT A RESULT WITH isError. The tool does not exist, so nothing ran —
		// and a model told "that failed" would try it again with different
		// arguments.
		return reply(req.ID, nil, &rpcError{Code: codeInvalidParams, Message: err.Error()})
	case err != nil:
		return reply(req.ID, nil, &rpcError{Code: codeInternal, Message: err.Error()})
	}

	return reply(req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": out.Content}},
		"isError": out.Failed,
	}, nil)
}

func reply(id json.RawMessage, result any, failure *rpcError) []byte {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out, err := json.Marshal(response{JSONRPC: "2.0", ID: id, Result: result, Error: failure})
	if err != nil {
		// Nothing above puts anything unmarshalable in a reply, so this is a
		// bug rather than a case — and a hand-written error is still valid
		// JSON-RPC, which a panic would not be.
		return []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"this server could not encode its own answer"}}`)
	}
	return out
}
