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

// mcp_test drives the registry an `mcp` resource becomes: its tools are its
// access block, and calling one invokes the function it names.
//
// IT WORKS OFF A SPEC RATHER THAN A COMPILED FIXTURE, because adding an mcp to
// fixtures/basic churns the byte-for-byte decompile goldens — and what is being
// tested here is the mapping from grants to tools, which a spec states exactly.

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

// TestTOOLSARETHEACCESSBLOCK, and there is no second list.
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

// TestONLYWHATITMAYINVOKE. A grant for a cache is not a tool, and neither is a
// function granted without the verb.
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

// TestAREFUSEDTARGETISNOTPUBLISHED. Listing a tool this cloud will not serve
// costs a model a turn to learn what the listing already knew.
func TestAToolThisCloudWillNotServeIsNotListed(t *testing.T) {
	refused := toolGrant("legacy", "", "invoke")
	refused.Refused = "that runtime is not available here"

	tools, err := registry(t, toolGrant("checkout", "", "invoke"), refused).List(context.Background())
	require.NoError(t, err)

	require.Len(t, tools, 1)
	require.Equal(t, "checkout", tools[0].Name)
}

// TestCALLINGATOOLTHATWASNOTPUBLISHED is no-such-tool, whatever the reason it
// is absent.
func TestCallingSomethingNotPublishedIsNoSuchTool(t *testing.T) {
	r := registry(t, toolGrant("checkout", "", "invoke"))

	_, err := r.Call(context.Background(), "ghost", nil)
	require.ErrorIs(t, err, mcp.ErrNoSuchTool)

	refused := toolGrant("legacy", "", "invoke")
	refused.Refused = "no"
	_, err = registry(t, refused).Call(context.Background(), "legacy", nil)
	require.ErrorIs(t, err, mcp.ErrNoSuchTool, "a refused tool cannot be called either")
}

// TestAGETISDECLINEDBYNAME. A client that GETs is asking to open the stream
// half of Streamable HTTP, which this kind declines — so it is told rather than
// left waiting on an empty answer.
func TestAGetIsToldThisServerIsStateless(t *testing.T) {
	s := &Service{}
	res, err := s.serveMCP(context.Background(),
		Target{Kind: mcpKind}, invoke.Spec{}, &invoke.Request{Method: http.MethodGet})
	require.NoError(t, err)

	require.Equal(t, http.StatusMethodNotAllowed, res.Status)
	require.Equal(t, []string{http.MethodPost}, res.Headers["Allow"])
}

// TestANOTIFICATIONIS202, because a message with no id expects no body.
func TestANotificationIsAcceptedWithNothingInIt(t *testing.T) {
	s := &Service{}
	res, err := s.serveMCP(context.Background(), Target{Kind: mcpKind}, invoke.Spec{},
		&invoke.Request{Method: http.MethodPost, Body: []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)})
	require.NoError(t, err)

	require.Equal(t, http.StatusAccepted, res.Status)
	require.Empty(t, res.Body)
}

// TestTHECACHINGFIELDSARETHEVOCABULARYS, answered as Cache-Control because that
// is the mechanism everything between here and the caller implements.
func TestTheListingsCacheScopeReachesTheAnswer(t *testing.T) {
	for _, c := range []struct {
		scope, ttl, want string
	}{
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

// TestTHEANSWERISJSON, which is what a Streamable HTTP client expects from a
// POST it did not ask to upgrade.
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

// TestAnInvokeGrantCarriesTheFunctionItRESOLVEDTO — the defect this pair of
// tests exists for, and the reason the ones above did not catch it: they build
// a spec by hand, so they agree with whatever the code believes a grant holds.
// These go through Resolve against the vocabulary's own fixture.
//
// A TOOL IS PUBLISHED UNDER THE AUTHOR'S LABEL, and Call used to build its
// target out of that label in the mcp's own scope. `notify` sits in /shop and
// reaches `relay` in `local`, so the label alone names nothing — resolving it is
// the composer's job, where the compiled project is open.
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

// TestTheADDRESSISWHATCALLGOESTO. The registry reads what the composer
// recorded, so a tool whose target lives elsewhere is invoked where it lives.
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
	require.Contains(t, reached, "shop/prod/function/"+relayID(t, h),
		"the function in /shop/local ran, which the label alone could never have reached")
}

// relayID is what `relay` compiled to, read off the fixture rather than
// written down here — an id in a test is a copy of the compiler's output that
// stops agreeing with it.
func relayID(t *testing.T, h *harness) string {
	t.Helper()
	spec := resolveIn(t, h, []string{"shop", "local"}, "function", "relay")
	return spec.Name[len("shop/prod/function/"):]
}
