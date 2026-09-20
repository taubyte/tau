package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

// fleet answers for nodes that do not exist: what each holds, and whether it
// means to keep it.
type answers map[string]keeper

func (a answers) ask(_ context.Context, peer string, _ address) (keeper, error) {
	held, known := a[peer]
	if !known {
		return keeper{}, errors.New("no route to host")
	}
	return held, nil
}

// keeping is a node that holds exactly what the departing node holds and means
// to keep it — the only answer that may authorise a drop.
func keeping(peers ...string) answers {
	out := answers{}
	for _, p := range peers {
		out[p] = keeper{keys: 3, sum: []byte("same"), held: true, keeps: true}
	}
	return out
}

func inRegion(region string, peers ...string) map[string][]string {
	return map[string][]string{region: peers}
}

// TestANodeLetsGoOnlyOnceSomebodyElseHasIt.
func TestReleaseWaitsForTheDigestToMatch(t *testing.T) {
	at, ctx := at("shop", "QmSessions"), t.Context()

	require.True(t, release(ctx, at, 3, []byte("same"),
		inRegion("us", "us1", "us2"), 2, keeping("us1", "us2").ask))

	// ONE OF TWO IS NOT THE FLOOR.
	require.False(t, release(ctx, at, 3, []byte("same"),
		inRegion("us", "us1", "us2"), 2, keeping("us1").ask))
}

// TestTheOutageCase, which is the whole argument for this gate: eu goes dark, an
// operator repins to us, us nodes are alive and desired but their source was
// down so they received nothing, eu comes back and must NOT drop the only copies.
func TestANodeComingBackDoesNotDropWhatNobodyElseGot(t *testing.T) {
	empty := answers{
		"us1": {keys: 0, sum: []byte("empty"), held: true, keeps: true},
		"us2": {keys: 0, sum: []byte("empty"), held: true, keeps: true},
	}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		inRegion("us", "us1", "us2"), 2, empty.ask),
		"the data was deleted because its replacements were alive")
}

// TestANODETHATISLEAVINGISNOTAKEEPER, which is the rule that stops two nodes
// deleting a database between them.
//
// Three ways their inputs diverge put each node outside its own desired set
// while inside the other's: a pin that moved between register revisions, two
// nodes relabelled inside one freshness window, and — not narrow at all — two
// nodes draining at once, which is a rolling restart. In every one of them each
// node is releasing BECAUSE it computed itself not desired, so each answers
// keeps=false and neither may go.
func TestTwoNodesThatEachThinkTheOtherKeepsBothStay(t *testing.T) {
	at, ctx := at("shop", "QmSessions"), t.Context()
	const key = "shop/prod/kv/QmSessions"

	// Both hold it, and both have begun letting go — each having computed that
	// the OTHER is the desired holder.
	a, p := newHoldings(), newHoldings()
	a.hold(key)
	p.hold(key)
	require.True(t, a.begin(key))
	require.True(t, p.begin(key))

	// Each answers for itself, and a node that is leaving says so.
	answerFor := func(h *holdings) digester {
		return func(context.Context, string, address) (keeper, error) {
			return keeper{keys: 3, sum: []byte("same"), held: true, keeps: h.keeps(key)}, nil
		}
	}

	require.False(t, release(ctx, at, 3, []byte("same"), inRegion("eu", "p"), 1, answerFor(p)),
		"A deleted its copy on the word of a node that was itself leaving")
	require.False(t, release(ctx, at, 3, []byte("same"), inRegion("us", "a"), 1, answerFor(a)),
		"P deleted its copy on the word of a node that was itself leaving")

	// AND THEY BOTH STILL HOLD IT. Neither went, so the next sweep can resolve
	// it once their inputs heal.
	a.stay(key)
	p.stay(key)
	require.True(t, a.keeps(key))
	require.True(t, p.keeps(key))
}

// TestHoldingACopyIsNotKeepingIt. A node mid-release holds the bytes, and
// counting that as a reason to leave is exactly the cycle above.
func TestAHeldButNotKeptCopyDoesNotAuthoriseADrop(t *testing.T) {
	mid := answers{
		"us1": {keys: 3, sum: []byte("same"), held: true, keeps: false},
		"us2": {keys: 3, sum: []byte("same"), held: true, keeps: true},
	}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		inRegion("us", "us1", "us2"), 2, mid.ask))
}

// TestANodeThatDoesNotHoldItIsNotAKeeper, whatever it intends. `keeps` without
// `held` is a node that means to hold a copy it has not got.
func TestIntendingToHoldIsNotHolding(t *testing.T) {
	intending := answers{"us1": {keys: 3, sum: []byte("same"), held: false, keeps: true}}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		inRegion("us", "us1"), 1, intending.ask))
}

// TestUnreachableIsNotMaybe.
func TestANodeThatCannotBeAskedHasNotConverged(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		inRegion("us", "us1", "us2"), 2, answers{}.ask))
}

// TestReleaseRefusesWhenNobodyIsDesired: placement returns an empty set for a
// region no node serves rather than filling it from elsewhere, and letting go
// there deletes a copy nothing is replacing.
func TestReleaseRefusesWhenNobodyIsDesired(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		map[string][]string{}, 2, keeping("us1", "us2").ask))
}

// TestEACHREGIONCLEARSITSOWNBAR.
//
// A pin to eu and us means copies in BOTH, and the floor is how many in EACH.
// An eu node that lost its eu slot must not leave because the two us keepers
// converged — its actual replacement in eu has not synced, and the region would
// sit below the floor the pin promised.
func TestAReplacementInAnotherRegionIsNotAReplacement(t *testing.T) {
	at, ctx := at("shop", "QmSessions"), t.Context()
	desired := map[string][]string{"eu": {"eu2", "eu3"}, "us": {"us1", "us2"}}

	require.False(t, release(ctx, at, 3, []byte("same"), desired, 2, keeping("us1", "us2").ask),
		"an eu copy left on the strength of us keepers")
	require.True(t, release(ctx, at, 3, []byte("same"), desired, 2,
		keeping("eu2", "eu3", "us1", "us2").ask))
}

// TestASmallerRegionIsItsOwnBar, or a region with one node is a bar nothing
// clears and the copy is held forever.
func TestTheBarIsTheRegionWhenItIsSmallerThanTheFloor(t *testing.T) {
	require.True(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		inRegion("us", "us1"), 2, keeping("us1").ask))
}

// TestTheDesiredSetExcludesMe, or a node comparing its digest against its own
// would converge with itself and let go.
func TestMovedNamesEverybodyButMe(t *testing.T) {
	byRegion := map[string][]placement.Member{"eu": members("eu1", "eu2", "eu3")}
	at := at("shop", "QmSessions")

	held := holders(at, byRegion, []string{"eu"}, 2)["eu"]
	require.Len(t, held, 2)

	desired, keep := moved(at, byRegion, []string{"eu"}, 2, held[0], "eu")
	require.True(t, keep)
	require.Nil(t, desired)

	var out string
	for _, id := range []string{"eu1", "eu2", "eu3"} {
		if id != held[0] && id != held[1] {
			out = id
		}
	}
	desired, keep = moved(at, byRegion, []string{"eu"}, 2, out, "eu")
	require.False(t, keep)
	require.ElementsMatch(t, held, desired["eu"], "the desired set is per region")
	require.NotContains(t, desired["eu"], out)
}

// TestOneReleaseAtATimePerDatabase, or two sweeps would ask on behalf of the
// same copy and each count the other's asks.
func TestAReleaseAlreadyUnderwayIsNotBegunTwice(t *testing.T) {
	h := newHoldings()
	const key = "shop/prod/kv/QmSessions"

	require.False(t, h.begin(key), "a database this node does not hold")
	h.hold(key)
	require.True(t, h.begin(key))
	require.False(t, h.begin(key))

	h.stay(key)
	require.True(t, h.keeps(key))
	h.gone(key)
	require.False(t, h.keeps(key))
}
