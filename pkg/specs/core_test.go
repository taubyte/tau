package specs_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// core_test covers the addressing the generated readers sit on: turning a name
// into a resource, an id back into an address, and a declared namespace into
// the chain a lookup walks.

// TestANAMESPACEISRESOLVEDINONEDIRECTION. An absolute one ignores the asker
// entirely — the leading "/" is in the grammar for exactly that — and a
// relative one hangs off whoever asked.
func TestAScopeResolvesFromWhoAsked(t *testing.T) {
	require.Equal(t, []string{"shop"}, specs.ScopeOf([]string{"shop"}, ""),
		"an omitted namespace is the asker's own scope")
	require.Equal(t, []string{"shop", "local"}, specs.ScopeOf([]string{"shop"}, "local"),
		"a relative one hangs off the asker")
	require.Equal(t, []string{"billing", "ledger"}, specs.ScopeOf([]string{"shop"}, "/billing/ledger"),
		"an absolute one ignores the asker, which is what the leading slash is for")
	require.Empty(t, specs.ScopeOf(nil, "/"),
		"the root is a chain of nothing, not a chain of one empty name")
}

// TestANIDGOESBACKTOANADDRESS. A reference resolves to an id and a reachable
// name arrives as one, so both need the other direction.
func TestAnIdLocatesBackToItsAddress(t *testing.T) {
	p := load(t)

	_, id, err := p.Resource([]string{"shop"}, "function", "notify")
	require.NoError(t, err)

	kind, scope, name, ok := p.Locate(id)
	require.True(t, ok)
	require.Equal(t, "function", kind)
	require.Equal(t, []string{"shop"}, scope)
	require.Equal(t, "notify", name)

	_, _, _, ok = p.Locate("QmNothingWasEverFiledUnderThis")
	require.False(t, ok, "an id nobody filed is not an address")
}

// TestAHANDLEISTHEDEPLOYMENTANDTHERESOURCE, which is what a tier opens a
// backing by — two deployments of one resource are two backings.
func TestAHandleCarriesTheDeployment(t *testing.T) {
	require.Equal(t, "shop/prod/QmThing", specs.Handle("shop", "prod", "QmThing"))
	require.NotEqual(t, specs.Handle("shop", "prod", "QmThing"), specs.Handle("shop", "preview", "QmThing"),
		"one resource in two environments is two handles")
}

// TestAMAPISREADWHERETHEREISONE, and is not one where the value is a scalar —
// which is the difference between a block and a bare path in this vocabulary.
func TestAMapReadsANestedBlock(t *testing.T) {
	p := load(t)

	fn, _, err := p.Resource(nil, "function", "reserve_stock")
	require.NoError(t, err)

	build, ok := fn.Map("build")
	require.True(t, ok)
	require.Equal(t, "commands", build["kind"])

	_, ok = fn.Map("source")
	require.False(t, ok, "the fixture writes the bare form, which is a string")

	_, ok = fn.Map("nothing", "here")
	require.False(t, ok)
}

// TestANENTRYSTARGETISITSLABELUNLESSITSAYSOTHERWISE — the one way to reach two
// resources that share a name in different namespaces.
func TestAReachTargetsItsLabelOrItsName(t *testing.T) {
	p := load(t)

	notify, _, err := p.Resource([]string{"shop"}, "function", "notify")
	require.NoError(t, err)

	reaches := notify.Reaches("function")
	require.NotEmpty(t, reaches)
	for _, reach := range reaches {
		require.Equal(t, reach.Label, reach.Target(),
			"the fixture writes no name override, so the label is the target")
	}
}

// TestAHOSTISFOUNDBYTHENAMEACALLERTYPED, however they typed it.
func TestAHostIsMatchedCaseInsensitively(t *testing.T) {
	p := load(t)

	hosts := p.Hosts()
	require.NotEmpty(t, hosts, "the fixture declares ingress")

	// IT IS INDEXED BY THE FQDN and not by the resource's name: a caller
	// arrives with a name a resolver gave it, which is the host, and the
	// resource that answers is what the lookup hands back.
	var found bool
	for _, h := range hosts {
		if h.Fqdn == "" {
			continue
		}
		require.NotEmpty(t, p.At(h.Fqdn), "%q was not found by its own fqdn", h.Fqdn)
		require.NotEmpty(t, p.At(strings.ToUpper(h.Fqdn)), "a name is matched however it was typed")
		found = true
		break
	}
	require.True(t, found, "the fixture declares a host with an fqdn")
	require.Empty(t, p.At("nothing.example.invalid"))
}

// TestANamespaceIsScopeOfReadBackwards: what a consumer renders is what an
// author would have typed, and what ScopeOf resolves it back to.
func TestANamespaceIsScopeOfReadBackwards(t *testing.T) {
	type testCase struct {
		scope []string
		name  string
		want  string
	}
	for _, one := range []testCase{
		{nil, "nightly", "/nightly"},
		{[]string{"billing"}, "nightly", "/billing/nightly"},
		{[]string{"shop", "billing"}, "nightly", "/shop/billing/nightly"},
	} {
		path := specs.NamespaceOf(one.scope, one.name)
		require.Equal(t, one.want, path)

		// The leading slash is the whole claim: an asker anywhere resolves it
		// to the same place.
		require.Equal(t, append(append([]string{}, one.scope...), one.name),
			specs.ScopeOf([]string{"somewhere", "else"}, path))
	}
}

// TestAHANDLEREADSBACKASWHATITWASBUILTFROM. Every consumer of a handle takes it
// apart again, and two spellings of one format agree by coincidence and diverge
// on the first edit to either — which is the argument Handle already makes about
// being here at all.
func TestAHandleReadsBackAsWhatItWasBuiltFrom(t *testing.T) {
	type testCase struct{ project, environment, id string }
	for _, one := range []testCase{
		{"shop", "prod", "QmSessions"},
		{"shop", "prod-shadow", "QmA"},
		{"a", "b", "c"},
	} {
		project, environment, id := specs.SplitHandle(specs.Handle(one.project, one.environment, one.id))
		require.Equal(t, one.project, project)
		require.Equal(t, one.environment, environment)
		require.Equal(t, one.id, id)
	}
}

// TestASHORTHANDLEANSWERSEMPTYRATHERTHANREFUSING, and the caller decides what
// that means: a name that resolves to nothing is a bug to report where the
// resource can be named.
func TestASplitHandleThatNamesNoDeployment(t *testing.T) {
	type testCase struct {
		handle                   string
		project, environment, id string
	}
	for _, one := range []testCase{
		{"", "", "", ""},
		{"nonsense", "nonsense", "", ""},
		{"shop/prod", "shop", "prod", ""},
		{"shop//QmA", "shop", "", "QmA"},
		// THE REST IS THE ID, slashes included: an addressed handle carries the
		// instance after the resource, and cutting at the third separator would
		// hand back a resource id that is not one.
		{"shop/prod/QmA/room-42", "shop", "prod", "QmA/room-42"},
	} {
		project, environment, id := specs.SplitHandle(one.handle)
		require.Equal(t, one.project, project, one.handle)
		require.Equal(t, one.environment, environment, one.handle)
		require.Equal(t, one.id, id, one.handle)
	}
}
