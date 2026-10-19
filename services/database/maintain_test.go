package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
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

// TestANodeLetsGoOnlyOnceSomebodyElseHasIt.
func TestReleaseWaitsForTheDigestToMatch(t *testing.T) {
	at, ctx := at("shop", "QmSessions"), t.Context()

	require.True(t, release(ctx, at, 3, []byte("same"),
		[]string{"us1", "us2"}, 2, keeping("us1", "us2").ask))

	// ONE OF TWO IS NOT THE FLOOR.
	require.False(t, release(ctx, at, 3, []byte("same"),
		[]string{"us1", "us2"}, 2, keeping("us1").ask))
}

// TestTheOutageCase, which is the whole argument for this gate: the holders go
// dark, membership moves the database, the new holders are alive and desired
// but their source was down so they received nothing, and the old holders come
// back and must NOT drop the only copies.
func TestANodeComingBackDoesNotDropWhatNobodyElseGot(t *testing.T) {
	empty := answers{
		"us1": {keys: 0, sum: []byte("empty"), held: true, keeps: true},
		"us2": {keys: 0, sum: []byte("empty"), held: true, keeps: true},
	}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, empty.ask),
		"the data was deleted because its replacements were alive")
}

// TestANODETHATISLEAVINGISNOTAKEEPER, which is the rule that stops two nodes
// deleting a database between them.
//
// Three ways their inputs diverge put each node outside its own desired set
// while inside the other's: a floor that moved between register revisions, and
// — not narrow at all — two nodes draining at once, which is a rolling restart. In every one of them each
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

	require.False(t, release(ctx, at, 3, []byte("same"), []string{"p"}, 1, answerFor(p)),
		"A deleted its copy on the word of a node that was itself leaving")
	require.False(t, release(ctx, at, 3, []byte("same"), []string{"a"}, 1, answerFor(a)),
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
		[]string{"us1", "us2"}, 2, mid.ask))
}

// TestANodeThatDoesNotHoldItIsNotAKeeper, whatever it intends. `keeps` without
// `held` is a node that means to hold a copy it has not got.
func TestIntendingToHoldIsNotHolding(t *testing.T) {
	intending := answers{"us1": {keys: 3, sum: []byte("same"), held: false, keeps: true}}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1"}, 1, intending.ask))
}

// TestUnreachableIsNotMaybe.
func TestANodeThatCannotBeAskedHasNotConverged(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, answers{}.ask))
}

// TestReleaseRefusesWhenNobodyIsDesired: letting go with no replacement deletes
// a copy nothing is replacing.
func TestReleaseRefusesWhenNobodyIsDesired(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		nil, 2, keeping("us1", "us2").ask))
}

// TestASmallerFleetIsItsOwnBar, or a fleet with one replacement is a bar nothing
// clears and the copy is held forever.
func TestTheBarIsTheFleetWhenItIsSmallerThanTheFloor(t *testing.T) {
	require.True(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1"}, 2, keeping("us1").ask))
}

// TestTheDesiredSetExcludesMe, or a node comparing its digest against its own
// would converge with itself and let go.
func TestMovedNamesEverybodyButMe(t *testing.T) {
	fleet := members("eu1", "eu2", "eu3")
	at := at("shop", "QmSessions")

	held := holders(at, fleet, 2)
	require.Len(t, held, 2)

	desired, keep := moved(at, fleet, 2, held[0])
	require.True(t, keep)
	require.Nil(t, desired)

	var out string
	for _, id := range []string{"eu1", "eu2", "eu3"} {
		if id != held[0] && id != held[1] {
			out = id
		}
	}
	desired, keep = moved(at, fleet, 2, out)
	require.False(t, keep)
	require.ElementsMatch(t, held, desired)
	require.NotContains(t, desired, out)
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

// TestAWRITEISREFUSEDONLYWHILETHEBYTESAREGOING.
//
// A node letting go of a database is still that database's holder until it is
// not, so refusing writes for the length of a digest round would be an outage
// per sweep. What is refused is the sealed window — from the moment the gate
// has been cleared until the copy is gone — which is the only stretch where a
// write would land in a store nothing will read again.
func TestAWriteIsRefusedOnlyWhileTheCopyIsSealed(t *testing.T) {
	h := newHoldings()
	const key = "shop/prod/kv/QmSessions"

	h.hold(key)
	require.True(t, h.writing(key))
	h.wrote(key)

	require.True(t, h.begin(key))
	require.True(t, h.writing(key), "a write was refused before anything had been decided")
	h.wrote(key)

	sealed, clean := h.seal(key)
	require.True(t, sealed)
	require.False(t, clean, "a write landed during the release and the gate's answer was reused anyway")
	require.False(t, h.writing(key), "a write was accepted into a copy about to be deleted")

	h.stay(key)
	require.True(t, h.writing(key), "a release that backed out left the database unwritable")
}

// TestNOTHINGISSEALEDOVERAHALFDONEWRITE, or the row goes with the delete: the
// write is already past the fence and lands after the digest the peers answered.
func TestASealIsRefusedWhileAWriteIsInFlight(t *testing.T) {
	h := newHoldings()
	const key = "shop/prod/kv/QmSessions"

	h.hold(key)
	require.True(t, h.begin(key))
	require.True(t, h.writing(key))

	sealed, _ := h.seal(key)
	require.False(t, sealed, "the copy was frozen while something was still writing into it")

	h.wrote(key)
	sealed, clean := h.seal(key)
	require.True(t, sealed)
	require.False(t, clean)
}

// TestARELEASENOBODYWROTEINTOISCLEAN, which is the common case and must cost
// nothing: no second digest round, no refused write.
func TestAnUntouchedCopySealsClean(t *testing.T) {
	h := newHoldings()
	const key = "shop/prod/kv/QmSessions"

	h.hold(key)
	require.True(t, h.begin(key))

	sealed, clean := h.seal(key)
	require.True(t, sealed)
	require.True(t, clean)
}

// TestTHEFENCEISNOTATABLEAPEERCANGROW.
//
// The key a write is fenced under is the CALLER's address. A fence that
// recorded one entry per address asked for would be a map any peer could grow
// without limit by naming databases this deployment does not declare, so
// resolving comes before the fence and an unresolvable write records nothing.
func TestAWriteToAnUndeclaredDatabaseRecordsNothing(t *testing.T) {
	holds := &says{held: true, onDisk: []database.Spec{sessionsSpec()}}
	s := sweeping(t, holds, members("me"), 1)

	at := address{project: projectID, environment: "prod", id: "QmNobodyDeclaresThis"}
	require.Error(t, s.mutate(t.Context(), at, func(database.KVConn) error {
		return nil
	}))

	s.own.mu.Lock()
	defer s.own.mu.Unlock()
	require.Empty(t, s.own.at, "an address nobody declared left an entry behind")
}
