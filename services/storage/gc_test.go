package storage

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/stretchr/testify/require"
)

func openGates(observed time.Time) Gates {
	return Gates{
		HoldsShard:           true,
		DesiredUnderStable:   true,
		CoHoldersMerged:      true,
		ReferencesDefinitive: true,
		TombstoneObservedAt:  observed,
		ProvenHolders:        3,
	}
}

func TestEveryGateClosesByName(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	observed := now.Add(-48 * time.Hour)
	grace := 24 * time.Hour

	require.NoError(t, openGates(observed).Open(DeadObject, now, grace, Policy{Replicas: 3, Known: true}))
	require.NoError(t, openGates(observed).Open(MisplacementDrop, now, grace, Policy{Replicas: 3, Known: true}))

	type testCase struct {
		name    string
		mutate  func(*Gates)
		purpose Purpose
		says    string
	}
	for _, c := range []testCase{
		{"no shard", func(g *Gates) { g.HoldsShard = false }, DeadObject, "does not hold the referencing shard"},
		{"not desired", func(g *Gates) { g.DesiredUnderStable = false }, DeadObject, "not HRW-desired"},
		{"co-holder behind", func(g *Gates) { g.CoHoldersMerged = false }, DeadObject, "has not merged"},
		{"references unknown", func(g *Gates) { g.ReferencesDefinitive = false }, DeadObject, "unknown rather than dead"},
		{"never observed", func(g *Gates) { g.TombstoneObservedAt = time.Time{} }, DeadObject, "never observed the tombstone"},
		{"grace running", func(g *Gates) { g.TombstoneObservedAt = now.Add(-time.Hour) }, DeadObject, "grace is"},
		{"no shard, drop", func(g *Gates) { g.HoldsShard = false }, MisplacementDrop, "does not hold the referencing shard"},
		{"unproven", func(g *Gates) { g.ProvenHolders = 2 }, MisplacementDrop, "proved possession"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := openGates(observed)
			c.mutate(&g)
			err := g.Open(c.purpose, now, grace, Policy{Replicas: 3, Known: true})
			require.ErrorIs(t, err, ErrGateClosed)
			require.Contains(t, err.Error(), c.says)
			require.Contains(t, err.Error(), c.purpose.String())
		})
	}

	err := openGates(observed).Open(Purpose(99), now, grace, Policy{Replicas: 3, Known: true})
	require.ErrorIs(t, err, ErrGateClosed)
	require.Contains(t, err.Error(), "purpose(99)")
}

func TestTheTwoPurposesAskDifferentQuestions(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	grace := 24 * time.Hour

	// An object nobody has ever tombstoned cannot be dead-object collected,
	// and CAN be dropped as a misplacement once others prove they hold it.
	g := openGates(time.Time{})
	require.ErrorIs(t, g.Open(DeadObject, now, grace, Policy{Replicas: 3, Known: true}), ErrGateClosed)
	require.NoError(t, g.Open(MisplacementDrop, now, grace, Policy{Replicas: 3, Known: true}))

	// And an object every gate is open for EXCEPT possession is collectable as
	// dead and not droppable as a misplacement.
	g = openGates(now.Add(-48 * time.Hour))
	g.ProvenHolders = 0
	require.NoError(t, g.Open(DeadObject, now, grace, Policy{Replicas: 3, Known: true}))
	require.ErrorIs(t, g.Open(MisplacementDrop, now, grace, Policy{Replicas: 3, Known: true}), ErrGateClosed)
}

func TestGraceIsMeasuredFromLocalObservation(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	grace := 24 * time.Hour

	// Exactly at the window it opens; one nanosecond short it does not.
	g := openGates(now.Add(-grace))
	require.NoError(t, g.Open(DeadObject, now, grace, Policy{Replicas: 3, Known: true}))

	g = openGates(now.Add(-grace + time.Nanosecond))
	require.ErrorIs(t, g.Open(DeadObject, now, grace, Policy{Replicas: 3, Known: true}), ErrGateClosed)
}

// dag builds a small DAG in memBlocks and returns the roots.
func dag(t *testing.T, blocks *memBlocks, spec map[string][]string) map[string]cid.Cid {
	t.Helper()
	ids := map[string]cid.Cid{}
	for label := range spec {
		ids[label] = testCID(t, label)
	}
	for label, links := range spec {
		var to []cid.Cid
		for _, l := range links {
			c, ok := ids[l]
			require.True(t, ok, "link %q not in the spec", l)
			to = append(to, c)
		}
		blocks.add(ids[label], to...)
	}
	return ids
}

func sweeper(t *testing.T, clk *fixedClock) (*Sweeper, *Local, *memBlocks) {
	t.Helper()
	l, _ := newLocal(t, clk)
	blocks := newMemBlocks()
	s, err := NewSweeper(l, blocks, nil)
	require.NoError(t, err)
	return s, l, blocks
}

func TestNewSweeperNeedsBothHalves(t *testing.T) {
	l, _ := newLocal(t, newFixedClock(0))
	_, err := NewSweeper(nil, newMemBlocks(), nil)
	require.Error(t, err)
	_, err = NewSweeper(l, nil, nil)
	require.Error(t, err)
	s, err := NewSweeper(l, newMemBlocks(), nil)
	require.NoError(t, err)
	require.NotNil(t, s.marker)
}

func TestTheSweepFreesWhatNothingReferences(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)
	a := addr(t)

	ids := dag(t, blocks, map[string][]string{
		"live-root": {"live-a", "live-b"},
		"live-a":    {"live-leaf"},
		"live-b":    {},
		"live-leaf": {},
		"dead-root": {"dead-leaf"},
		"dead-leaf": {},
	})
	for _, c := range ids {
		require.NoError(t, l.Imported(ctx, c))
	}
	require.NoError(t, l.Reference(ctx, ids["live-root"], a, "kept.bin", 10))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, got.Roots)
	require.Equal(t, 4, got.Marked, "the WHOLE dag is marked, not just the root")
	require.Equal(t, 6, got.Indexed)
	require.Equal(t, 2, got.Deleted)
	require.Empty(t, got.Incomplete)

	// Every leaf of the live object survives.
	for _, label := range []string{"live-root", "live-a", "live-b", "live-leaf"} {
		have, err := blocks.Has(ctx, ids[label])
		require.NoError(t, err)
		require.True(t, have, "%s was freed and is live", label)
	}
	for _, label := range []string{"dead-root", "dead-leaf"} {
		have, err := blocks.Has(ctx, ids[label])
		require.NoError(t, err)
		require.False(t, have, "%s is dead and was kept", label)
	}

	// And the index rows follow the blocks.
	indexed, err := l.IndexedBlocks(ctx)
	require.NoError(t, err)
	require.Len(t, indexed, 4)

	// A second sweep is a no-op.
	got, err = s.Sweep(ctx)
	require.NoError(t, err)
	require.Zero(t, got.Deleted)
}

func TestTheSweepNeverTouchesABlockItDidNotIndex(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)
	a := addr(t)

	ids := dag(t, blocks, map[string][]string{
		"mine":       {},
		"kvdb-delta": {},
		"someone":    {},
	})
	// Only "mine" is this service's, and it is dead.
	require.NoError(t, l.Imported(ctx, ids["mine"]))
	require.NoError(t, l.Reference(ctx, testCID(t, "elsewhere"), a, "x", 1))
	blocks.add(testCID(t, "elsewhere"))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, got.Deleted)

	for _, label := range []string{"kvdb-delta", "someone"} {
		have, err := blocks.Has(ctx, ids[label])
		require.NoError(t, err)
		require.True(t, have, "%s belongs to another consumer and was deleted", label)
	}
	require.Equal(t, 2, blocks.held()-1, "only the foreign blocks and the live root remain")
}

func TestASharedBlockSurvivesTheDeleteOfOneTenantsObject(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)

	alice := instance(t, "alice", "prod", "assets", "main")
	bob := instance(t, "bob", "prod", "assets", "main")

	ids := dag(t, blocks, map[string][]string{"shared": {"chunk"}, "chunk": {}})
	for _, c := range ids {
		require.NoError(t, l.Imported(ctx, c))
	}
	require.NoError(t, l.Reference(ctx, ids["shared"], alice, "logo.png", 10))
	require.NoError(t, l.Reference(ctx, ids["shared"], bob, "logo.png", 20))

	// Alice deletes. Bob's reference is what keeps the bytes.
	require.NoError(t, l.Dereference(ctx, ids["shared"], alice, "logo.png", 10))
	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Zero(t, got.Deleted)
	require.Equal(t, 1, got.Roots, "one distinct root, reached from one surviving reference")

	// Bob deletes too. Now the whole DAG goes.
	require.NoError(t, l.Dereference(ctx, ids["shared"], bob, "logo.png", 20))
	got, err = s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, got.Deleted)
	require.Zero(t, blocks.held())
}

func TestAnIncompleteWalkDeletesNothing(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)
	a := addr(t)

	// This node holds the root and the grandchild, but not the child.
	root, child, grandchild := testCID(t, "root"), testCID(t, "child"), testCID(t, "grandchild")
	blocks.add(root, child)
	blocks.add(grandchild)
	require.NoError(t, l.Imported(ctx, root))
	require.NoError(t, l.Imported(ctx, grandchild))
	require.NoError(t, l.Reference(ctx, root, a, "obj", 1))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, []cid.Cid{root}, got.Incomplete)
	require.Zero(t, got.Deleted, "an unknown set cannot be subtracted from")
	require.Equal(t, 2, blocks.held())

	// It is self-healing rather than an operator step: once the missing block
	// is back, the next cycle completes and frees what is genuinely dead.
	blocks.add(child, grandchild)
	got, err = s.Sweep(ctx)
	require.NoError(t, err)
	require.Empty(t, got.Incomplete)
	require.Zero(t, got.Deleted, "and everything under the live root is live")

	dead := testCID(t, "dead")
	blocks.add(dead)
	require.NoError(t, l.Imported(ctx, dead))
	got, err = s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, got.Deleted)
}

func TestACycleDoesNotHangTheWalk(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)
	a := addr(t)

	// A content-addressed DAG cannot contain a cycle, so this is defence
	// against a corrupt or hostile block rather than an expected shape — and
	// the failure it prevents is a sweep that never returns.
	x, y := testCID(t, "x"), testCID(t, "y")
	blocks.add(x, y)
	blocks.add(y, x)
	require.NoError(t, l.Imported(ctx, x))
	require.NoError(t, l.Imported(ctx, y))
	require.NoError(t, l.Reference(ctx, x, a, "obj", 1))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, got.Marked)
	require.Zero(t, got.Deleted)
}

func TestADiamondMarksEachBlockOnce(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)
	a := addr(t)

	ids := dag(t, blocks, map[string][]string{
		"root": {"left", "right"},
		"left": {"leaf"}, "right": {"leaf"}, "leaf": {},
	})
	for _, c := range ids {
		require.NoError(t, l.Imported(ctx, c))
	}
	require.NoError(t, l.Reference(ctx, ids["root"], a, "obj", 1))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, got.Marked, "a shared leaf is marked once, not twice")
}

func TestABloomMarkerNeverForgetsALiveBlock(t *testing.T) {
	m, err := NewBloomMarker(2000, 0.01)
	require.NoError(t, err)

	var marked []cid.Cid
	for i := 0; i < 2000; i++ {
		c := testCID(t, fmt.Sprintf("live-%d", i))
		m.Mark(c)
		marked = append(marked, c)
	}
	for _, c := range marked {
		require.True(t, m.Marked(c), "%s was marked and forgotten", c)
	}

	_, err = NewBloomMarker(10, 0)
	require.Error(t, err)
	_, err = NewBloomMarker(10, 1)
	require.Error(t, err)
	_, err = NewBloomMarker(0, 0.01)
	require.NoError(t, err, "a zero hint is floored, not refused")
}

func TestAFalsePositiveLeaksRatherThanDeletes(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	l, _ := newLocal(t, clk)
	blocks := newMemBlocks()
	s, err := NewSweeper(l, blocks, func(int) (Marker, error) { return alwaysMarked{}, nil })
	require.NoError(t, err)
	a := addr(t)

	ids := dag(t, blocks, map[string][]string{"live": {}, "dead": {}})
	for _, c := range ids {
		require.NoError(t, l.Imported(ctx, c))
	}
	require.NoError(t, l.Reference(ctx, ids["live"], a, "obj", 1))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Zero(t, got.Deleted, "space leaks; data does not")
	require.Equal(t, 2, blocks.held())
}

func TestARealBloomFalsePositiveKeepsADeadBlock(t *testing.T) {
	m, err := NewBloomMarker(1, 0.5)
	require.NoError(t, err)
	live := testCID(t, "live")
	m.Mark(live)

	var collided cid.Cid
	for i := 0; i < 20000 && !collided.Defined(); i++ {
		c := testCID(t, fmt.Sprintf("dead-%d", i))
		if m.Marked(c) {
			collided = c
		}
	}
	require.True(t, collided.Defined(), "a 1-entry filter at 0.5 is expected to collide within 20000 tries")
	require.True(t, m.Marked(live), "and the live block is still marked")
}

func TestANodeKilledMidSweepLosesNothingAndTheNextSweepConverges(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)
	a := addr(t)

	spec := map[string][]string{"live": {"live-leaf"}, "live-leaf": {}}
	for i := 0; i < 8; i++ {
		spec[fmt.Sprintf("dead-%d", i)] = nil
	}
	ids := dag(t, blocks, spec)
	for _, c := range ids {
		require.NoError(t, l.Imported(ctx, c))
	}
	require.NoError(t, l.Reference(ctx, ids["live"], a, "obj", 1))

	// The node dies after freeing three blocks.
	freed := 0
	blocks.failDelete = func(cid.Cid) error {
		freed++
		if freed > 3 {
			return errInjected
		}
		return nil
	}
	_, err := s.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)

	// Nothing live went, and the index still names everything that survived.
	for _, label := range []string{"live", "live-leaf"} {
		have, err := blocks.Has(ctx, ids[label])
		require.NoError(t, err)
		require.True(t, have)
	}
	indexed, err := l.IndexedBlocks(ctx)
	require.NoError(t, err)
	for _, c := range indexed {
		have, err := blocks.Has(ctx, c)
		require.NoError(t, err)
		require.True(t, have, "an index row names a block that is gone: %s", c)
	}

	// It comes back and finishes the job. No operator step, no repair mode.
	blocks.failDelete = nil
	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, got.Deleted)
	require.Equal(t, 2, blocks.held())
}

func TestTheBlockGoesBeforeItsIndexRow(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)

	dead := testCID(t, "dead")
	blocks.add(dead)
	require.NoError(t, l.Imported(ctx, dead))

	// The crash: the block is freed, the index row is not.
	require.NoError(t, blocks.Delete(ctx, dead))
	indexed, err := l.IndexedBlocks(ctx)
	require.NoError(t, err)
	require.Len(t, indexed, 1)

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, got.Deleted)
	indexed, err = l.IndexedBlocks(ctx)
	require.NoError(t, err)
	require.Empty(t, indexed, "the next sweep drops the stale row")
}

func TestASweepPropagatesEveryFailure(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)

	// The reference listing fails.
	failing, err := NewLocal(failingStore{}, clk.now)
	require.NoError(t, err)
	s, err := NewSweeper(failing, newMemBlocks(), nil)
	require.NoError(t, err)
	_, err = s.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)

	// The marker cannot be built.
	l, _ := newLocal(t, clk)
	s, err = NewSweeper(l, newMemBlocks(), func(int) (Marker, error) { return nil, errInjected })
	require.NoError(t, err)
	_, err = s.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)

	// A link read fails.
	s2, l2, blocks := sweeper(t, clk)
	a := addr(t)
	root := testCID(t, "root")
	blocks.add(root)
	require.NoError(t, l2.Imported(ctx, root))
	require.NoError(t, l2.Reference(ctx, root, a, "obj", 1))
	blocks.failLinks = func(cid.Cid) error { return errInjected }
	_, err = s2.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)

	// A cancelled context stops the walk rather than finishing it silently.
	blocks.failLinks = nil
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s2.Sweep(cancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestBlocksHasNoDoorForEnumeratingTheBlockstore(t *testing.T) {
	tp := reflect.TypeOf((*Blocks)(nil)).Elem()
	var methods []string
	for i := 0; i < tp.NumMethod(); i++ {
		methods = append(methods, tp.Method(i).Name)
	}
	sort.Strings(methods)
	require.Equal(t, []string{"Block", "Delete", "Has", "Links"}, methods,
		"Blocks may not grow AllKeysChan or any other enumeration: the node's blockstore "+
			"holds pkg/kvdb's own Merkle-DAG blocks and every other consumer's. Each method "+
			"takes the one cid it acts on")
}

func TestPurposeNamesItself(t *testing.T) {
	require.Equal(t, "dead object", DeadObject.String())
	require.Equal(t, "misplacement drop", MisplacementDrop.String())
	require.Equal(t, "purpose(7)", Purpose(7).String())
}

func TestTheExactMarkerIsExact(t *testing.T) {
	m := NewExactMarker(4)
	a, b := testCID(t, "a"), testCID(t, "b")
	require.False(t, m.Marked(a))
	m.Mark(a)
	require.True(t, m.Marked(a))
	require.False(t, m.Marked(b))
}

// alwaysMarked is the worst legal marker: every answer is a false positive.
type alwaysMarked struct{}

func (alwaysMarked) Mark(cid.Cid)        {}
func (alwaysMarked) Marked(cid.Cid) bool { return true }
