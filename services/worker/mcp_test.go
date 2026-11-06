package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/mcp"
)

// Works off a spec rather than a compiled fixture: adding an mcp to fixtures/basic
// churns the decompile goldens, and the grants-to-tools mapping is what is tested.

func toolGrant(label, description string, ops ...string) invoke.Grant {
	g := invoke.Grant{
		Kind: "function", Label: label,
		Handle: "shop/prod/Qm" + label, Ops: ops,
	}
	if description != "" {
		g.Settings = map[string]string{settingDescription: description}
	}
	return g
}

func registry(t *testing.T, grants ...invoke.Grant) *mcpTools {
	t.Helper()
	return &mcpTools{
		target: Target{Project: "shop", Environment: "prod", Kind: mcpKind, Name: "tools"},
		spec:   invoke.Spec{Name: "shop/prod/mcp/tools", Reaches: grants},
	}
}

func TestAnMcpPublishesTheFunctionsItMayInvoke(t *testing.T) {
	tools, err := registry(t,
		toolGrant("checkout", "places an order", "invoke"),
		toolGrant("ledger", "reads the books", "invoke"),
	).List(context.Background())
	require.NoError(t, err)

	require.Len(t, tools, 2)
	require.Equal(t, "checkout", tools[0].Name, "the label the author wrote, which is what a caller sees")
	require.Equal(t, "places an order", tools[0].Description)
}

func TestAGrantThatIsNotAnInvokableFunctionIsNotATool(t *testing.T) {
	cache := invoke.Grant{Kind: "cache", Label: "sessions", Ops: []string{"get", "put"}}

	tools, err := registry(t,
		toolGrant("checkout", "", "invoke"),
		toolGrant("readonly", "", "describe"),
		cache,
	).List(context.Background())
	require.NoError(t, err)

	require.Len(t, tools, 1)
	require.Equal(t, "checkout", tools[0].Name)
}

func TestAToolThisCloudWillNotServeIsNotListed(t *testing.T) {
	refused := toolGrant("legacy", "", "invoke")
	refused.Refused = "that runtime is not available here"

	tools, err := registry(t, toolGrant("checkout", "", "invoke"), refused).List(context.Background())
	require.NoError(t, err)

	require.Len(t, tools, 1)
	require.Equal(t, "checkout", tools[0].Name)
}

func TestCallingSomethingNotPublishedIsNoSuchTool(t *testing.T) {
	r := registry(t, toolGrant("checkout", "", "invoke"))

	_, err := r.Call(context.Background(), "ghost", nil)
	require.ErrorIs(t, err, mcp.ErrNoSuchTool)

	refused := toolGrant("legacy", "", "invoke")
	refused.Refused = "no"
	_, err = registry(t, refused).Call(context.Background(), "legacy", nil)
	require.ErrorIs(t, err, mcp.ErrNoSuchTool, "a refused tool cannot be called either")
}

func TestAGetIsToldThisServerIsStateless(t *testing.T) {
	s := &Service{}
	res, err := s.serveMCP(context.Background(),
		Target{Kind: mcpKind}, invoke.Spec{}, &invoke.Request{Method: http.MethodGet})
	require.NoError(t, err)

	require.Equal(t, http.StatusMethodNotAllowed, res.Status)
	require.Equal(t, []string{http.MethodPost}, res.Headers["Allow"])
}

func TestANotificationIsAcceptedWithNothingInIt(t *testing.T) {
	s := &Service{}
	res, err := s.serveMCP(context.Background(), Target{Kind: mcpKind}, invoke.Spec{},
		&invoke.Request{Method: http.MethodPost, Body: []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)})
	require.NoError(t, err)

	require.Equal(t, http.StatusAccepted, res.Status)
	require.Empty(t, res.Body)
}

func TestTheListingsCacheScopeReachesTheAnswer(t *testing.T) {
	type testCase struct {
		scope, ttl, want string
	}
	for _, c := range []testCase{
		{"", "", "no-store"},
		{"none", "5m", "no-store"},
		{"public", "5m", "public, max-age=300"},
		{"principal", "30s", "private, max-age=30"},
		{"public", "", "public, max-age=0"},
		{"public", "not a duration", "public, max-age=0"},
	} {
		spec := invoke.Spec{Settings: map[string]string{
			settingMCPCacheScope: c.scope,
			settingMCPCacheTTL:   c.ttl,
		}}
		require.Equal(t, c.want, mcpHeaders(spec)["Cache-Control"][0],
			"scope %q ttl %q", c.scope, c.ttl)
	}
}

func TestTheAnswerSaysItIsJson(t *testing.T) {
	s := &Service{}
	res, err := s.serveMCP(context.Background(), Target{Kind: mcpKind}, invoke.Spec{},
		&invoke.Request{Method: http.MethodPost, Body: []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, res.Status)
	require.Equal(t, []string{"application/json"}, res.Headers["Content-Type"])

	var out map[string]any
	require.NoError(t, json.Unmarshal(res.Body, &out))
	require.Equal(t, "2.0", out["jsonrpc"])
}

func TestAnInvokeGrantCarriesTheFunctionItResolvedTo(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"shop"}, "function", "notify")

	var relay *invoke.Grant
	for i, g := range spec.Reaches {
		if g.Kind == mcpToolKind && g.Label == "relay" {
			relay = &spec.Reaches[i]
			break
		}
	}
	require.NotNil(t, relay, "the fixture's /shop/notify reaches relay in local")

	require.Equal(t, "relay", relay.Settings[settingInvokeName])
	require.Equal(t, "/shop/local", relay.Settings[settingInvokeScope],
		"where it was FOUND, not the scope of whoever asked - a caller building a target out of the label alone looked in /shop and found nothing")
}

func TestCallTargetsTheResolvedAddressAndNotTheLabel(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"shop"}, "function", "notify")

	tools := &mcpTools{
		service: h.Worker,
		target:  Target{Project: "shop", Environment: "prod", Kind: mcpKind, Name: "tools"},
		spec:    spec,
	}

	res, err := tools.Call(t.Context(), "relay", json.RawMessage(`{"order":1}`))
	require.NoError(t, err)
	require.False(t, res.Failed, "the function is in the fixture and the tier is fake: %s", res.Content)

	var reached []string
	for _, s := range h.Runtime.specs() {
		reached = append(reached, s.Name)
	}
	require.Contains(t, reached, resolveIn(t, h, []string{"shop", "local"}, "function", "relay").Name,
		"the function in /shop/local ran, which the label alone could never have reached")
}
