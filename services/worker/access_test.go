package worker

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/project"
)

// EVERY TEST HERE READS THE VOCABULARY'S OWN FIXTURE, compiled. What an access
// block means is the compiler's, and what a grant carries is what a tier
// enforces — so a hand-written project would be this package's idea of both.

// cacheGrants are the cache entries of a resolved spec, by label.
func cacheGrants(t *testing.T, spec invoke.Spec) map[string]invoke.Grant {
	t.Helper()

	out := map[string]invoke.Grant{}
	for _, g := range spec.Reaches {
		if g.Kind == cacheKind {
			out[g.Label] = g
		}
	}
	return out
}

// reachFor is one entry, for the rules checked directly against a target
// document rather than through the fixture.
func reachFor(label string) project.Reach { return project.Reach{Kind: cacheKind, Label: label} }

func resolveIn(t *testing.T, h *harness, scope []string, kind, name string) invoke.Spec {
	t.Helper()

	spec, err := h.Worker.Resolve(t.Context(), Target{
		Project: "shop", Environment: "prod", Scope: scope, Kind: kind, Name: name,
	})
	require.NoError(t, err)
	return spec
}

// TestTheAccessBlockBecomesGrants — the compiled `cache:` block reaches the
// tier as something it can enforce, with the verbs the author asked for.
func TestACacheAccessEntryBecomesAGrant(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"billing"}, "function", "invoice")

	grants := cacheGrants(t, spec)
	require.Contains(t, grants, "sessions")
	require.Equal(t, []string{"get"}, grants["sessions"].Ops, "read only, as declared")
	require.Empty(t, grants["sessions"].Refused, "billing's own sessions is crdt and servable")
}

// TestTheHandleCarriesTheRESOLVEDId, not the label. The fixture has three
// caches called `sessions` in three namespaces, so a handle built from the
// label would hand all three the same store.
func TestOneLabelInTwoNamespacesIsTwoHandles(t *testing.T) {
	h := newHarness(t)

	// invoice sits in /billing and writes `sessions` with no namespace, so it
	// gets billing's own. checkout sits at the root and writes the same label
	// with `namespace: /shop`, so it gets shop's.
	billing := cacheGrants(t, resolveIn(t, h, []string{"billing"}, "function", "invoice"))
	root := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Contains(t, billing, "sessions")
	require.Contains(t, root, "sessions")
	require.NotEqual(t, billing["sessions"].Handle, root["sessions"].Handle,
		"one label, two namespaces, two caches")
}

// TestAHandleNamesTheDeployment, because a cache is per environment: staging's
// `sessions` and production's are the same declaration and different data.
func TestAHandleIsScopedToTheDeployment(t *testing.T) {
	h := newHarness(t)
	spec := resolveIn(t, h, []string{"billing"}, "function", "invoice")

	handle := cacheGrants(t, spec)["sessions"].Handle
	require.Contains(t, handle, "shop/prod/", "the project and the deployment are in front of the id")
	require.NotContains(t, handle, "sessions", "and the label is not")
}

// ── what this cloud will not serve ───────────────────────────────────────────

// TestAnAuthorizedCacheIsARefusedGrantAndNotARefusedWorkload.
//
// The fixture's `orders` cache names an authorizer, and nothing here calls one.
// Serving it would grant access the target's own configuration says somebody
// else decides; refusing the WORKLOAD would take a function down over one of
// the things it reaches. So the resource runs and that one target does not.
func TestACacheWithAnAuthorizerIsRefusedByName(t *testing.T) {
	h := newHarness(t)

	// checkout reaches `orders`, which is guarded. IT RESOLVES — that is the
	// point: the function runs, and one of the things it touches does not.
	spec := resolveIn(t, h, nil, "function", "checkout")

	grants := cacheGrants(t, spec)
	require.Contains(t, grants, "orders")

	refused := grants["orders"]
	require.NotEmpty(t, refused.Refused)
	require.Contains(t, refused.Refused, "authorizer")
	require.Contains(t, refused.Refused, "tier_check", "it names which one")
	require.Equal(t, []string{"get", "put"}, refused.Ops, "what was asked for is still carried")
	require.False(t, refused.Allows("get"), "a refused grant allows nothing, whatever its ops say")
}

// TestTheFixtureItselfHitsBothRefusals, which is worth stating: the
// vocabulary's richest function reaches two caches and this cloud can serve
// neither, one for an authorizer and one for its consensus. Both are one line
// for an author to change, and neither stops the function running.
func TestTheCanonicalFixtureHitsBothRefusals(t *testing.T) {
	h := newHarness(t)
	grants := cacheGrants(t, resolveIn(t, h, nil, "function", "checkout"))

	require.Len(t, grants, 2)
	require.Contains(t, grants["orders"].Refused, "authorizer")
	require.Contains(t, grants["sessions"].Refused, "single-writer")
}

// TestSingleWriterIsRefusedRatherThanSilentlyDowngraded. An author who wrote
// single-writer asked for "nothing is silently discarded"; a CRDT merges
// concurrent writes last-writer-wins and the loser is gone with no error.
func TestASingleWriterCacheIsRefusedByName(t *testing.T) {
	reason := unservable(reachFor("ledger"), project.Resource{"consensus": singleWriter})

	require.Contains(t, reason, "single-writer", "it names what was declared")
	require.Contains(t, reason, "last-writer-wins", "and what it would have got instead")
	require.Contains(t, reason, `cache "ledger"`, "and which target")
}

// TestGossipIsServedRatherThanRefused, because a CRDT is STRONGER than gossip:
// gossip is eventually consistent and lossy, a CRDT is eventually consistent
// and not. An author who accepted a dropped entry gets one that arrives.
func TestAGossipCacheIsServed(t *testing.T) {
	require.Empty(t, unservable(reachFor("x"), project.Resource{"consensus": "gossip"}))
	require.Empty(t, unservable(reachFor("x"), project.Resource{"consensus": "crdt"}))
}

// TestTheAuthorizerCheckComesFirst, so a cache that is both guarded and
// single-writer says the thing an author has to fix first.
func TestAnAuthorizerIsNamedBeforeConsensus(t *testing.T) {
	both := project.Resource{
		"consensus": singleWriter,
		"authorizer": map[string]any{
			"name": "tier_check",
		},
	}
	require.Contains(t, unservable(reachFor("x"), both), "authorizer")
}

// TestAResourceThatReachesNothingCarriesNoGrants, which is most of them.
func TestAResourceWithNoCacheAccessHasNoCacheGrants(t *testing.T) {
	h := newHarness(t)

	// charge_card reaches a secret and nothing else.
	spec := resolveIn(t, h, nil, "function", "charge_card")
	require.Empty(t, cacheGrants(t, spec))
	require.Empty(t, spec.Reaches, "and no grant of another kind is invented")
}
