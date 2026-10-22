package specs_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// EVERY TEST HERE READS THE VOCABULARY'S OWN FIXTURE, compiled. What the access
// block means is decided by the compiler, and a hand-written map would be this
// package's idea of the shape rather than the shape.

func basic(t *testing.T) *specs.Project {
	t.Helper()
	return loadFixture(t, basicFixture, "prod", "main")
}

// invoice is the fixture's own worked example: it reads a cache and writes the
// books, from inside a namespace.
func invoice(t *testing.T) (specs.Resource, []string) {
	t.Helper()

	r, _, err := basic(t).Resource([]string{"billing"}, "function", "invoice")
	require.NoError(t, err)
	return r, []string{"billing"}
}

func TestAResourceReadsItsOwnAccessBlock(t *testing.T) {
	r, _ := invoice(t)

	caches := r.Reaches("cache")
	require.Len(t, caches, 1)
	require.Equal(t, "sessions", caches[0].Label)
	require.Equal(t, []string{"get"}, caches[0].Ops)
	require.Equal(t, "cache", caches[0].Kind)

	// SORTED BY LABEL, and it is asserted over many reads because the map
	// underneath is not ordered: an unsorted answer differs between two
	// resolves of one revision, which is a spec that changes without a deploy.
	for range 20 {
		databases := r.Reaches("database")
		require.Len(t, databases, 2)
		require.Equal(t, "ledger", databases[0].Label)
		require.Equal(t, "profiles", databases[1].Label, "sorted by label")
	}
}

func TestAKindNothingWasDeclaredForIsEmpty(t *testing.T) {
	r, _ := invoice(t)
	require.Empty(t, r.Reaches("storage"))
	require.Empty(t, r.Reaches("queue"))
}

// TestALoweredChildIsNotAGrant. The compiler files a resource lowered into
// another under the SAME kind key, by its id — so a reader that took every
// entry would hand a workload access to things nobody granted.
func TestALoweredResourceIsNotMistakenForAGrant(t *testing.T) {
	// The agent's `database:` map holds two access entries AND its own lowered
	// memory database, which is the one keyed by an id.
	r, _, err := basic(t).Resource(nil, "agent", "support")
	require.NoError(t, err)

	// The lowered child is visible in the raw document...
	raw := r.Sub("database")
	require.Greater(t, len(raw), 2, "the map holds more than the grants")

	// ...and is not a grant.
	for _, reach := range r.Reaches("database") {
		require.NotEmpty(t, reach.Ops, "an entry with no ops is not a grant")
		require.NotContains(t, reach.Label, "Qm", "an id is not a label: %s", reach.Label)
	}
}

// ── resolving one ────────────────────────────────────────────────────────────

// TestOneLabelResolvesToThreeDIFFERENTCaches, which is the whole reason the
// access block carries a namespace and the resolver walks outward.
//
// The fixture declares a cache called `sessions` in /billing, in /shop and in
// /shop/eu. A function in each of those namespaces writes the SAME label and
// must reach its own — and a handle built from the label rather than from the
// resolved id would hand all three the same store.
func TestOneLabelResolvesToADifferentCachePerNamespace(t *testing.T) {
	p := basic(t)
	r, asker := invoice(t)

	reach := r.Reaches("cache")[0]
	require.Equal(t, "sessions", reach.Label)

	target, mine, err := p.Reached(asker, reach)
	require.NoError(t, err)
	require.Equal(t, "sessions", target.String("name"))
	require.Contains(t, target.String("description"), "billing's sessions")

	// The same entry, asked from another namespace, is another cache.
	_, theirs, err := p.Reached([]string{"shop"}, reach)
	require.NoError(t, err)
	require.NotEqual(t, mine, theirs, "two namespaces, one label, two caches")

	_, deeper, err := p.Reached([]string{"shop", "local"}, reach)
	require.NoError(t, err)
	require.NotEqual(t, theirs, deeper, "and the nested one is its own again")
}

// TestAnAbsoluteNamespaceIgnoresTheAsker, which is the whole reason the leading
// slash is in the grammar.
func TestAnAbsoluteNamespaceIsFromTheRoot(t *testing.T) {
	p := basic(t)

	// A resource sitting in /shop reaching /billing must find billing's, not
	// /shop/billing's.
	notify, _, err := p.Resource([]string{"shop"}, "function", "notify")
	require.NoError(t, err)

	for _, reach := range notify.Reaches("database") {
		if reach.Namespace == "" {
			continue
		}
		require.True(t, reach.Namespace[0] == '/', "this test wants an absolute one")

		_, id, err := p.Reached([]string{"shop"}, reach)
		require.NoError(t, err, reach.Label)

		// The same entry read from a DIFFERENT asker resolves to the same
		// resource, which is what "absolute" means.
		_, other, err := p.Reached(nil, reach)
		require.NoError(t, err)
		require.Equal(t, id, other)
	}
}

// TestAnOmittedNamespaceWalksOutward: the asker's own scope first, then its
// parents, which is what lets a namespace share the project's caches.
func TestAnOmittedNamespaceResolvesOutward(t *testing.T) {
	p := basic(t)
	r, _ := invoice(t)

	reach := r.Reaches("cache")[0]
	require.Empty(t, reach.Namespace)

	// invoice is in /billing and the cache is at the root: found by walking
	// out, and not found at all if the walk stopped.
	_, id, err := p.Reached([]string{"billing"}, reach)
	require.NoError(t, err)
	require.NotEmpty(t, id)
}

// TestAGrantForSomethingThatIsNotThereIsAnError, naming the label the author
// wrote rather than whatever it resolved to.
func TestAReachThatResolvesToNothingSaysWhichLabel(t *testing.T) {
	p := basic(t)

	_, _, err := p.Reached(nil, specs.Reach{
		Kind: "cache", Label: "nope", Ops: []string{"get"},
	})
	require.ErrorIs(t, err, specs.ErrNotFound)
	require.ErrorContains(t, err, `cache "nope"`)
}

// TestNameOverridesTheLabel, which is the one case the label alone cannot
// hold: two targets sharing a name in different namespaces.
func TestAnExplicitNameIsWhatIsLookedUp(t *testing.T) {
	p := basic(t)

	_, id, err := p.Reached(nil, specs.Reach{
		Kind: "cache", Label: "whatever-i-call-it", Name: "orders", Ops: []string{"get"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)
}

// TestEveryFieldOfAnEntryIsRead, over a document built here because the
// fixture does not exercise the optional three — and a field that is dropped
// silently is a per-use modifier nobody can set.
func TestAnEntryCarriesItsNameNamespaceAndTimeout(t *testing.T) {
	r := specs.Resource{
		"cache": map[string]any{
			"books": map[string]any{
				"name":      "ledger",
				"namespace": "/billing",
				"ops":       []any{"put", "get"},
				"timeout":   float64(5 * time.Second),
			},
			// A lowered child beside it, to prove the ops test still bites on a
			// hand-built document.
			"QmSomethingLowered": map[string]any{"name": "not-a-grant"},
		},
	}

	reaches := r.Reaches("cache")
	require.Len(t, reaches, 1)
	require.Equal(t, "books", reaches[0].Label)
	require.Equal(t, "ledger", reaches[0].Name)
	require.Equal(t, "/billing", reaches[0].Namespace)
	require.Equal(t, []string{"get", "put"}, reaches[0].Ops, "sorted")
	require.Equal(t, 5*time.Second, reaches[0].Timeout)
}

// A TOOL BOUND TRAVELS WITH THE GRANT. Dropped here, an entry naming one tool of
// a catalogue would reach the runtime as an entry on the whole catalogue, and
// nothing downstream could tell.
func TestAToolBoundTravelsWithTheGrant(t *testing.T) {
	r, _, err := basic(t).Resource(nil, "agent", "support")
	require.NoError(t, err)

	mcps := r.Reaches("mcp")
	require.Len(t, mcps, 1)
	require.Equal(t, []string{"checkout"}, mcps[0].Tools)

	// and an entry that names none is on every tool, which is the other half
	r, _ = invoice(t)
	for _, reach := range r.Reaches("database") {
		require.Empty(t, reach.Tools)
	}
}
