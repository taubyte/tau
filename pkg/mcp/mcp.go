// Package mcp serves the Model Context Protocol over one request and one answer.
//
// The `mcp` kind groups functions as tools, and what it groups is what it declares
// access to: no separate `tools:` list, which would be a grant path the target's
// authorizer never sees.
//
// It serves stateless Streamable HTTP (a POST answered with JSON): no session, no
// `Mcp-Session-Id`, so any node answers any call. What a tool is and how it runs are
// behind Tools.
//
// A missing method is a JSON-RPC error. A tool that failed is a successful result
// with `isError`, so the model can read it and react.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the protocol revision this server implements. It is answered, not
// negotiated: echoing the client's request would claim unsupported revisions.
const Version = "2025-06-18"

// Tool is one thing a client may call.
type Tool struct {
	Name        string
	Description string

	// Schema is JSON Schema for the arguments, as raw JSON. Empty is an object with no
	// declared properties: a tau function takes a body of undeclared shape.
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

// Serve answers one POSTed message. A notification gets no bytes: it expects no
// answer, which HTTP says with 202 and an empty body.
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
		// Tools only: the vocabulary has nothing to publish as resources or prompts.
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
			// An object with no declared properties: the body's shape is unknown, not empty.
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
		// Not a result with isError: nothing ran, and a model told "that failed" would retry
		// with different arguments.
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
