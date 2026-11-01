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

// `mcp` declares no engine: an MCP server is a protocol in front of functions that
// already run, so it is answered here, as node/step.go answers a workflow driver.
// Its tools are its access block; a separate tool list would be a grant path the
// target's authorizer never sees. A tool's arguments are the POST body, published
// with a property-less object schema.

const mcpKind = specs.KindMcp

// mcpToolKind is what an mcp groups: only `invoke` on a function is callable with
// arguments and an answer.
const mcpToolKind = specs.KindFunction

// mcpInvokeOp is the verb a tool is published for.
const mcpInvokeOp = "invoke"

// serveMCP answers one protocol message against this resource's tools.
func (s *Service) serveMCP(ctx context.Context, t Target, spec invoke.Spec, req *invoke.Request) (*invoke.Response, error) {
	// A GET asks for Streamable HTTP's stream half, which this kind declines out loud
	// rather than leaving the client waiting.
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

// mcpHeaders is the content type, and `tools/cache_scope` and `tools/cache_ttl` as
// Cache-Control. Set on every answer: the scope is the author's ceiling and the
// default `none` is `no-store`.
func mcpHeaders(spec invoke.Spec) map[string][]string {
	out := map[string][]string{"Content-Type": {"application/json"}}

	ttl := spec.Settings[settingMCPCacheTTL]
	switch spec.Settings[settingMCPCacheScope] {
	case specs.McpToolsCacheScopePublic:
		out["Cache-Control"] = []string{cacheControl("public", ttl)}
	case specs.McpToolsCacheScopePrincipal:
		// A principal-scoped listing depends on the caller, which is `private`.
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

// List is every function this mcp may invoke. A tool's name is the access-block
// label, which Call also looks up.
func (m *mcpTools) List(context.Context) ([]mcp.Tool, error) {
	var out []mcp.Tool
	for _, g := range m.spec.Reaches {
		if g.Kind != mcpToolKind || !g.Allows(mcpInvokeOp) {
			continue
		}
		if g.Refused != "" {
			// Only callable tools are published.
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

	// The label is what was published; the address is what is called, recorded by
	// reachesOf while the project was open. The asker is nil because the recorded scope
	// is where the function was found, not a path to walk.
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
		// The tool failed, not the call: isError lets an agent read it, where a JSON-RPC
		// error would read as a broken server.
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

	// settingDescription is the target's own description, which is how a model decides
	// whether to call a tool.
	settingDescription = specs.FunctionKeyDescription
)

func parseDurationSetting(raw string) (time.Duration, error) { return time.ParseDuration(raw) }
