package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

// answers is a fleet that says what each node holds, so a test can state the
// thing that actually matters: not who SHOULD hold a database, but who does.
type answers map[string]struct {
	keys uint64
	sum  []byte
	err  error
}

func (a answers) ask(_ context.Context, peer string, _ address) (uint64, []byte, error) {
	held, known := a[peer]
	if !known || held.err != nil {
		if known {
			return 0, nil, held.err
		}
		return 0, nil, errors.New("no route to host")
	}
	return held.keys, held.sum, nil
}

func converged(peers ...string) answers {
	out := answers{}
	for _, p := range peers {
		out[p] = struct {
			keys uint64
			sum  []byte
			err  error
		}{keys: 3, sum: []byte("same")}
	}
	return out
}

// TestANodeLetsGoOnlyOnceSomebodyElseHasIt.
func TestReleaseWaitsForTheDigestToMatch(t *testing.T) {
	at := at("shop", "QmSessions")
	ctx := t.Context()

	require.True(t, release(ctx, at, 3, []byte("same"), []string{"us1", "us2"}, 2, converged("us1", "us2").ask),
		"both replacements hold exactly this, and it still would not go")

	// ONE OF TWO IS NOT THE FLOOR.
	require.False(t, release(ctx, at, 3, []byte("same"), []string{"us1", "us2"}, 2, converged("us1").ask))
}

// TestTheOutageCase, which is the whole argument for this gate.
//
// eu goes dark, an operator repins to us to restore service, and us nodes are
// alive and are now the desired holders — but their source was down, so they
// received nothing. eu comes back, reads the new placement, and must NOT drop
// the only copies that ever existed.
func TestANodeComingBackDoesNotDropWhatNobodyElseGot(t *testing.T) {
	alive := answers{
		"us1": {keys: 0, sum: []byte("empty")},
		"us2": {keys: 0, sum: []byte("empty")},
	}

	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, alive.ask),
		"the data was deleted because its replacements were alive")
}

// TestUnreachableIsNotMaybe. The answer to "may I delete the data" defaults to
// no, every time, for every reason.
func TestANodeThatCannotBeAskedHasNotConverged(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1", "us2"}, 2, answers{}.ask))
}

// TestNobodyIsSupposedToHoldThis, which is not a move: placement returns an
// empty set for a pinned region no node serves rather than filling it from
// elsewhere, and letting go there deletes a copy nothing is replacing.
func TestReleaseRefusesWhenNobodyIsDesired(t *testing.T) {
	require.False(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		nil, 2, converged("us1", "us2").ask))
}

// TestASmallerRegionIsItsOwnFloor. Requiring two where one node serves the
// region is a bar nothing clears, and the copy would be held forever.
func TestTheBarIsTheRegionWhenItIsSmallerThanTheFloor(t *testing.T) {
	require.True(t, release(t.Context(), at("shop", "QmSessions"), 3, []byte("same"),
		[]string{"us1"}, 2, converged("us1").ask))
}

// TestTheDesiredSetExcludesMe, or a node comparing its digest against its own
// would converge with itself and let go.
func TestMovedNamesEverybodyButMe(t *testing.T) {
	byRegion := map[string][]placement.Member{
		"eu": members("eu1", "eu2", "eu3"),
	}
	at := at("shop", "QmSessions")

	held := holders(at, byRegion, []string{"eu"}, 2)["eu"]
	require.Len(t, held, 2)

	// A node that holds it keeps it, and is told no set.
	desired, keep := moved(at, byRegion, []string{"eu"}, 2, held[0], "eu")
	require.True(t, keep)
	require.Nil(t, desired)

	// The one that does not is told the others, and never itself.
	var out string
	for _, id := range []string{"eu1", "eu2", "eu3"} {
		if id != held[0] && id != held[1] {
			out = id
		}
	}
	desired, keep = moved(at, byRegion, []string{"eu"}, 2, out, "eu")
	require.False(t, keep)
	require.ElementsMatch(t, held, desired)
	require.NotContains(t, desired, out)
}
