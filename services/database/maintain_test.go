package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/database"
)

// answers stands in for nodes that do not exist: what each holds, and whether it
// keeps it.
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

func TestReleaseWaitsForTheDigestToMatch(t *testing.T) {
	at, ctx := at("shop", "QmSessions"), t.Context()

	require.True(t, release(ctx, at, 3, []byte("same"),
		[]string{"us1", "us2"}, 2, keeping("us1", "us2").ask))

	// ONE OF TWO IS NOT THE FLOOR.
	require.False(t, release(ctx, at, 3, []byte("same"),
		[]string{"us1", "us2"}, 2, keeping("us1").ask))
}

func TestANodeComingBackDoesNotDropWhatNobodyElseGot(t *testing.T) {
	empty := answers{
		"us1": {keys: 0, sum: []byte("empty"), held: true, keeps: true},
		"us2": {keys: 0, sum: []byte("empty"), held: true, keeps: true},
	}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, empty.ask),
		"the data was deleted because its replacements were alive")
}

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

func TestAHeldButNotKeptCopyDoesNotAuthoriseADrop(t *testing.T) {
	mid := answers{
		"us1": {keys: 3, sum: []byte("same"), held: true, keeps: false},
		"us2": {keys: 3, sum: []byte("same"), held: true, keeps: true},
	}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, mid.ask))
}

func TestIntendingToHoldIsNotHolding(t *testing.T) {
	intending := answers{"us1": {keys: 3, sum: []byte("same"), held: false, keeps: true}}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1"}, 1, intending.ask))
}

func TestANodeThatCannotBeAskedHasNotConverged(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, answers{}.ask))
}

func TestReleaseRefusesWhenNobodyIsDesired(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		nil, 2, keeping("us1", "us2").ask))
}

func TestTheBarIsTheFleetWhenItIsSmallerThanTheFloor(t *testing.T) {
	require.True(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1"}, 2, keeping("us1").ask))
}

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

func TestAnUntouchedCopySealsClean(t *testing.T) {
	h := newHoldings()
	const key = "shop/prod/kv/QmSessions"

	h.hold(key)
	require.True(t, h.begin(key))

	sealed, clean := h.seal(key)
	require.True(t, sealed)
	require.True(t, clean)
}

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
