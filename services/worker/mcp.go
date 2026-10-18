package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/mcp"
	"github.com/taubyte/tau/pkg/specs"
)

// mcp.go serves the vocabulary's `mcp` kind.
//
// # It has no engine, so this is where it runs
//
// Every kind that RUNS declares one — a function is wasm, an application a
// container — and `mcp` declares none, because there is nothing of the author's
// to start: an MCP server is a protocol in front of functions that already run.
// So it is answered here rather than dispatched to a tier, the way node/step.go
// answers a workflow's driver.
//
// # Its tools are its access block, and there is no second list
//
// An mcp that may `invoke` three functions publishes three tools. That falls
// out of the vocabulary rather than being designed: a separate `tools:` block
// would be a grant path the authorizer on the target never sees, which is the
// door the access rules exist to keep shut.
//
// # What a tool's arguments are
//
// The body. A tau function takes bytes and this vocabulary gives it no way to
// declare a shape for them, so every tool is published with an object schema
// that declares no properties — which says "an object, contents unspecified"
// rather than claiming it takes nothing. What the client sends as `arguments`
// is what the function is POSTed.

const mcpKind = specs.KindMcp

// mcpToolKind is what an mcp groups. One kind, deliberately: a tool is
// something a model may call with arguments and read an answer from, which is
// what `invoke` on a function means and what nothing else in this vocabulary
// offers.
const mcpToolKind = specs.KindFunction

// mcpInvokeOp is the verb a tool is published for.
const mcpInvokeOp = "invoke"

// serveMCP answers one protocol message against this resource's tools.
func (s *Service) serveMCP(ctx context.Context, t Target, spec invoke.Spec, req *invoke.Request) (*invoke.Response, error) {
	// THE PROTOCOL IS A POST. A client that GETs is asking to open the stream
	// half of Streamable HTTP, which this kind declines — so it is told, rather
	// than handed an empty answer it would wait on.
	if req.Method != "" && !strings.EqualFold(req.Method, http.MethodPost) {
		return &invoke.Response{
			Status:  http.StatusMethodNotAllowed,
			Headers: map[string][]string{"Allow": {http.MethodPost}},
			Body:    []byte("this mcp server is stateless: subscriptions are declined, so every call is a POST"),
		}, nil
	}

	answer, notification, err := mcp.Serve(ctx, &mcpTools{service: s, target: t, spec: spec}, req.Body)
	if err != nil {
		return nil, err
	}
	if notification {
		// A message with no id expects no answer, and HTTP's way of saying so
		// is 202 with nothing in it.
		return &invoke.Response{Status: http.StatusAccepted}, nil
	}

	return &invoke.Response{
		Status:  http.StatusOK,
		Headers: mcpHeaders(spec),
		Body:    answer,
	}, nil
}

// mcpHeaders is the content type, and what the resource said about caching.
//
// THE VOCABULARY'S OWN TWO FIELDS. `tools/cache_scope` says how widely a
// listing may be cached and `tools/cache_ttl` for how long, and they are
// answered as Cache-Control because that is the mechanism every client between
// here and the caller already implements.
//
// IT IS SET ON EVERY ANSWER AND NOT ONLY ON A LISTING, which is safe because
// the scope is the author's ceiling: `none` — the default — is `no-store`, and
// a resource that opted into caching opted in for its whole surface.
func mcpHeaders(spec invoke.Spec) map[string][]string {
	out := map[string][]string{"Content-Type": {"application/json"}}

	ttl := spec.Settings[settingMCPCacheTTL]
	switch spec.Settings[settingMCPCacheScope] {
	case specs.McpToolsCacheScopePublic:
		out["Cache-Control"] = []string{cacheControl("public", ttl)}
	case specs.McpToolsCacheScopePrincipal:
		// PER CALLER. `private` is HTTP's word for "one user's cache may hold
		// this and a shared one may not", which is what a principal-scoped
		// listing means: what a caller may see depends on who it is.
		out["Cache-Control"] = []string{cacheControl("private", ttl)}
	default:
		out["Cache-Control"] = []string{"no-store"}
	}
	return out
}

func cacheControl(scope, ttl string) string {
	seconds := "0"
	if ttl != "" {
		if d, err := parseDurationSetting(ttl); err == nil && d > 0 {
			seconds = strconv.FormatInt(int64(d.Seconds()), 10)
		}
	}
	return scope + ", max-age=" + seconds
}

// mcpTools is this resource's access block, as a tool registry.
type mcpTools struct {
	service *Service
	target  Target
	spec    invoke.Spec
}

// List is every function this mcp may invoke.
//
// THE LABEL IS THE TOOL'S NAME, which is what the author wrote in the access
// block — not the target's own name, which may differ and which a caller has no
// way to have seen. It is also what Call looks up, so the two cannot disagree.
func (m *mcpTools) List(context.Context) ([]mcp.Tool, error) {
	var out []mcp.Tool
	for _, g := range m.spec.Reaches {
		if g.Kind != mcpToolKind || !g.Allows(mcpInvokeOp) {
			continue
		}
		if g.Refused != "" {
			// PUBLISHED TOOLS ARE CALLABLE ONES. A tool this cloud will not
			// serve, listed, is a model choosing it and being told no — which
			// costs a turn to learn something the listing already knew.
			continue
		}
		out = append(out, mcp.Tool{
			Name:        g.Label,
			Description: g.Settings[settingDescription],
		})
	}
	return out, nil
}

// Call invokes one, with the arguments as its body.
func (m *mcpTools) Call(ctx context.Context, name string, arguments json.RawMessage) (mcp.Result, error) {
	var granted *invoke.Grant
	for i, g := range m.spec.Reaches {
		if g.Kind == mcpToolKind && g.Label == name && g.Allows(mcpInvokeOp) && g.Refused == "" {
			granted = &m.spec.Reaches[i]
			break
		}
	}
	if granted == nil {
		return mcp.Result{}, fmt.Errorf("%w: %q is not published by this server", mcp.ErrNoSuchTool, name)
	}

	body := []byte(arguments)
	if len(body) == 0 {
		body = []byte("{}")
	}

	// THE LABEL IS WHAT WAS PUBLISHED AND THE ADDRESS IS WHAT IS CALLED. A tool
	// is named after the author's access-block label, which is not the target's
	// name and need not be in this mcp's scope, so invoking `name` here in
	// `m.target.Scope` called whatever happened to sit at that name beside the
	// server — or nothing. reachesOf resolved the real one while the compiled
	// project was open; this reads what it recorded.
	//
	// AN ABSOLUTE NAMESPACE IGNORES THE ASKER, which is why the asker passed to
	// ScopeOf is nil: the recorded scope is where the function WAS FOUND, not a
	// path to walk from here.
	res, err := m.service.Invoke(ctx, Target{
		Project:     m.target.Project,
		Environment: m.target.Environment,
		Scope:       specs.ScopeOf(nil, granted.Settings[settingInvokeScope]),
		Kind:        mcpToolKind,
		Name:        granted.Settings[settingInvokeName],
	}, &invoke.Request{
		Method:  http.MethodPost,
		Path:    "/",
		Headers: map[string][]string{"Content-Type": {"application/json"}},
		Body:    body,
	})
	if err != nil {
		// THE TOOL FAILED AND THE CALL DID NOT. It reaches the model as a
		// result carrying isError, so an agent can read what went wrong and try
		// something else — where a JSON-RPC error would read as a broken server.
		return mcp.Result{Content: err.Error(), Failed: true}, nil
	}

	return mcp.Result{
		Content: string(res.Body),
		Failed:  res.Status >= 400,
	}, nil
}

// The vocabulary's own attribute paths, as they travel in a spec's settings.
const (
	settingMCPCacheScope = specs.McpToolsKeyCacheScope
	settingMCPCacheTTL   = specs.McpToolsKeyCacheTtl

	// settingDescription is the target's own description, carried so that a
	// tool published to a model has one. It is the single most load-bearing
	// string in a tool listing — it is how the model decides whether to call it.
	settingDescription = specs.FunctionKeyDescription
)

func parseDurationSetting(raw string) (time.Duration, error) { return time.ParseDuration(raw) }
