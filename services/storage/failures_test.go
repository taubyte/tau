package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-datastore/query"
	"github.com/stretchr/testify/require"
)

// Paths where the layer below misbehaves. Every one must be an error, never a zero,
// an empty list or a skipped row.

// midScanFailure returns entries whose second carries an error, which is how a
// datastore reports a failure that happens after the query was accepted.
type midScanFailure struct{ Datastore }

func (m midScanFailure) Query(_ context.Context, q query.Query) (query.Results, error) {
	return query.ResultsWithEntries(q, []query.Entry{}), nil
}

// erroringScan is the real shape: a Results channel carrying a Result whose
// Error is set.
type erroringScan struct{ Datastore }

func (e erroringScan) Query(_ context.Context, q query.Query) (query.Results, error) {
	return query.ResultsWithContext(q, func(_ context.Context, out chan<- query.Result) {
		out <- query.Result{Error: errInjected}
	}), nil
}

// roguePrefix answers with a row that is NOT under the namespace it was asked
// for. No backend in the tree does this; the point is that if one did, every
// reader here would refuse rather than mis-sum or mis-parse.
type roguePrefix struct {
	Datastore
	key string
}

func (r roguePrefix) Query(_ context.Context, q query.Query) (query.Results, error) {
	return query.ResultsWithEntries(q, []query.Entry{{Key: r.key, Value: []byte("1")}}), nil
}

// selectiveFailure fails one namespace and passes everything else through.
type selectiveFailure struct {
	Datastore
	queryPrefix  string
	deletePrefix string
}

func (s selectiveFailure) Query(ctx context.Context, q query.Query) (query.Results, error) {
	if s.queryPrefix != "" && strings.HasPrefix(q.Prefix, s.queryPrefix) {
		return nil, errInjected
	}
	return s.Datastore.Query(ctx, q)
}

func (s selectiveFailure) Delete(ctx context.Context, k ds.Key) error {
	if s.deletePrefix != "" && strings.HasPrefix(k.String(), s.deletePrefix) {
		return errInjected
	}
	return s.Datastore.Delete(ctx, k)
}

func TestAScanThatFailsPartwayIsAnErrorNotAnEmptyResult(t *testing.T) {
	ctx := context.Background()
	a := addr(t)
	i := index{store: erroringScan{Datastore: newOrderedMem()}}

	_, err := i.Current(ctx, a, "obj")
	require.ErrorIs(t, err, errInjected, "an object with a failing scan must not read as missing")
	_, err = i.Versions(ctx, a, "obj", 0)
	require.ErrorIs(t, err, errInjected)
	_, err = i.Usage(ctx, a)
	require.ErrorIs(t, err, errInjected)

	l, err := NewLocal(erroringScan{Datastore: newOrderedMem()}, nil)
	require.NoError(t, err)
	_, err = l.IndexedBlocks(ctx)
	require.ErrorIs(t, err, errInjected)
	_, err = l.References(ctx)
	require.ErrorIs(t, err, errInjected)
}

func TestAnEmptyScanIsNotFoundRatherThanAnError(t *testing.T) {
	ctx := context.Background()
	a := addr(t)
	i := index{store: midScanFailure{Datastore: newOrderedMem()}}

	_, err := i.Current(ctx, a, "obj")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestARowOutsideItsNamespaceIsRefused(t *testing.T) {
	ctx := context.Background()
	a := addr(t)

	i := index{store: roguePrefix{Datastore: newOrderedMem(), key: "/somewhere/else"}}
	_, err := i.Usage(ctx, a)
	require.ErrorIs(t, err, ErrNotThisServicesRow)

	_, err = NewLocal(newOrderedMem(), nil)
	require.NoError(t, err)

	rogue, err := NewLocal(roguePrefix{Datastore: newOrderedMem(), key: localDirty + "/a/b"}, nil)
	require.NoError(t, err)
	_, err = rogue.DirtyInstances(ctx)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestACorruptRowUnderOneRootIsAnError(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	l, err := NewLocal(store, nil)
	require.NoError(t, err)

	root := testCID(t, "root")
	seg, err := cidSegment(root)
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(localRefs+"/"+seg+"/a.b.c.d/zz/ffffffffffffffff"), []byte("1")))

	_, err = l.ReferencesTo(ctx, root)
	require.Error(t, err)
}

func TestATombstoneObservationRefusesAnUnencodableName(t *testing.T) {
	ctx := context.Background()
	l, _ := newLocal(t, newFixedClock(0))
	a := addr(t)

	_, err := l.ObservedTombstone(ctx, a, "", 1)
	require.ErrorIs(t, err, ErrEmptyObjectName)
	require.ErrorIs(t, l.ForgetTombstone(ctx, a, "", 1), ErrEmptyObjectName)
}

func TestAFailedObservationWriteIsAnError(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	store.failPut = func(key string) error {
		if strings.HasPrefix(key, localTombs) {
			return errInjected
		}
		return nil
	}
	l, err := NewLocal(store, nil)
	require.NoError(t, err)

	_, err = l.ObservedTombstone(ctx, addr(t), "gone", 1)
	require.ErrorIs(t, err, errInjected)
}

func TestAtRefusesAForeignRowAtTheStamp(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	prefix, err := a.objectPrefix("obj")
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(prefix+"/"+stampSegment(5)+"/!!"), []byte("junk")))

	_, err = i.At(ctx, a, "obj", 5)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestTheSweepStopsWhenItsOwnBookkeepingFails(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	a := addr(t)

	// The block index cannot be enumerated.
	base := newOrderedMem()
	l, err := NewLocal(selectiveFailure{Datastore: base, queryPrefix: localBlocks}, clk.now)
	require.NoError(t, err)
	s, err := NewSweeper(l, newMemBlocks(), nil)
	require.NoError(t, err)
	_, err = s.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)

	// The index row cannot be dropped after the block was freed. The block is
	// gone and the row remains, which is exactly the crash window the ordering
	// was chosen for: the next sweep resolves it.
	base2 := newOrderedMem()
	l2, err := NewLocal(selectiveFailure{Datastore: base2, deletePrefix: localBlocks}, clk.now)
	require.NoError(t, err)
	blocks := newMemBlocks()
	dead := testCID(t, "dead")
	blocks.add(dead)
	require.NoError(t, l2.Imported(ctx, dead))
	s2, err := NewSweeper(l2, blocks, nil)
	require.NoError(t, err)
	_, err = s2.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)
	require.Zero(t, blocks.held(), "the block went first, which is the order that is recoverable")

	// And the possession check failing stops the walk.
	s3, l3, blocks3 := sweeper(t, clk)
	root := testCID(t, "root")
	blocks3.add(root)
	require.NoError(t, l3.Reference(ctx, root, a, "obj", 1))
	blocks3.failHas = func(cid.Cid) error { return errInjected }
	_, err = s3.Sweep(ctx)
	require.ErrorIs(t, err, errInjected)
}

func TestTwoReferencesToOneRootWalkItOnce(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	s, l, blocks := sweeper(t, clk)

	ids := dag(t, blocks, map[string][]string{"shared": {"chunk"}, "chunk": {}})
	for _, c := range ids {
		require.NoError(t, l.Imported(ctx, c))
	}
	alice := instance(t, "alice", "prod", "assets", "main")
	bob := instance(t, "bob", "prod", "assets", "main")
	require.NoError(t, l.Reference(ctx, ids["shared"], alice, "logo.png", 10))
	require.NoError(t, l.Reference(ctx, ids["shared"], bob, "logo.png", 20))

	got, err := s.Sweep(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, got.Roots, "two references, one root, one walk")
	require.Equal(t, 2, got.Marked)
	require.Zero(t, got.Deleted)
}
