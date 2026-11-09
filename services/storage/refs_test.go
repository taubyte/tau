package storage

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
)

func newLocal(t *testing.T, clk *fixedClock) (*Local, *orderedMem) {
	t.Helper()
	store := newOrderedMem()
	l, err := NewLocal(store, clk.now)
	require.NoError(t, err)
	return l, store
}

func TestNewLocalNeedsAStoreAndDefaultsItsClock(t *testing.T) {
	_, err := NewLocal(nil, nil)
	require.Error(t, err)

	l, err := NewLocal(newOrderedMem(), nil)
	require.NoError(t, err)
	require.NotNil(t, l.now)
}

func TestAnInstanceIsOneSegmentAndRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	for i := 0; i < 1500; i++ {
		p, e, b, inst := randomBytesName(rng), randomBytesName(rng), randomBytesName(rng), randomBytesName(rng)
		a, err := NewAddress(p, e, b, inst)
		require.NoError(t, err)

		seg, err := instanceSegment(a)
		require.NoError(t, err)
		require.NotContains(t, seg, "/", "an instance is one path segment")
		require.Len(t, strings.Split(seg, "."), 4, "and it splits back into exactly four")

		back, err := addressFromInstanceSegment(seg)
		require.NoError(t, err)
		require.Equal(t, a.String(), back.String())
		require.True(t, clean(localRefs+"/x/"+seg))
	}
}

func TestAddressFromInstanceSegmentRefusesAForeignSegment(t *testing.T) {
	_, err := addressFromInstanceSegment("a.b.c")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = addressFromInstanceSegment("!!.b.c.d")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = addressFromInstanceSegment("...")
	require.Error(t, err)
}

func TestCidSegmentRefusesWhatIsNotOneSegment(t *testing.T) {
	_, err := cidSegment(cid.Undef)
	require.ErrorIs(t, err, ErrNotThisServicesRow)

	c := testCID(t, "block")
	seg, err := cidSegment(c)
	require.NoError(t, err)
	require.NotContains(t, seg, "/")
	require.Equal(t, c.String(), seg)
}

func TestReferenceRowsRoundTrip(t *testing.T) {
	a := addr(t)
	root := testCID(t, "root")
	for _, name := range realisticNames() {
		key, err := refKey(root, a, name, 4242)
		require.NoError(t, err)
		require.True(t, clean(key))

		got, err := parseRefKey(key)
		require.NoError(t, err)
		require.Equal(t, root, got.Root)
		require.Equal(t, name, got.Name)
		require.Equal(t, uint64(4242), got.Stamp)
		require.Equal(t, a.String(), got.Address.String())
	}
}

func TestParseRefKeyRefusesEverythingItDidNotWrite(t *testing.T) {
	a := addr(t)
	root := testCID(t, "root")
	good, err := refKey(root, a, "obj", 1)
	require.NoError(t, err)
	parts := strings.Split(strings.TrimPrefix(good, localRefs+"/"), "/")

	for _, k := range []string{
		"/elsewhere/x",
		localRefs + "/a/b/c",
		good + "/extra",
		localRefs + "/not-a-cid/" + strings.Join(parts[1:], "/"),
		localRefs + "/" + parts[0] + "/a.b.c/" + strings.Join(parts[2:], "/"),
		localRefs + "/" + strings.Join(parts[:2], "/") + "/zz/" + parts[3],
		localRefs + "/" + strings.Join(parts[:3], "/") + "/ff",
	} {
		_, err := parseRefKey(k)
		require.Error(t, err, "accepted %q", k)
	}
}

func TestBlockIndexRowsRoundTrip(t *testing.T) {
	c := testCID(t, "block")
	key, err := blockKey(c)
	require.NoError(t, err)
	require.True(t, clean(key))
	got, err := cidFromBlockKey(key)
	require.NoError(t, err)
	require.Equal(t, c, got)

	_, err = cidFromBlockKey("/elsewhere/x")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = cidFromBlockKey(localBlocks + "/a/b")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = cidFromBlockKey(localBlocks + "/not-a-cid")
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, err = blockKey(cid.Undef)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestTheBlockIndexIsWhatTheSweepEnumerates(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	l, store := newLocal(t, clk)

	blocks := []cid.Cid{testCID(t, "a"), testCID(t, "b"), testCID(t, "c")}
	for _, c := range blocks {
		require.NoError(t, l.Imported(ctx, c))
	}

	// A block somebody ELSE put in the node's blockstore — pkg/kvdb's own
	// Merkle-DAG blocks are exactly this — is not in the index and therefore
	// not enumerable here. That is the whole point of the index existing.
	foreign := testCID(t, "kvdb-delta")
	require.NoError(t, store.Put(ctx, ds.RawKey("/some/other/consumer/"+foreign.String()), []byte("x")))

	got, err := l.IndexedBlocks(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, blocks, got)

	require.NoError(t, l.Forget(ctx, blocks[0]))
	got, err = l.IndexedBlocks(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, blocks[1:], got)

	require.ErrorIs(t, l.Imported(ctx, cid.Undef), ErrNotThisServicesRow)
	require.ErrorIs(t, l.Forget(ctx, cid.Undef), ErrNotThisServicesRow)
}

func TestTwoTenantsPushingIdenticalBytesLeaveTwoReferences(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	l, _ := newLocal(t, clk)

	root := testCID(t, "shared-bytes")
	alice := instance(t, "alice", "prod", "assets", "main")
	bob := instance(t, "bob", "prod", "assets", "main")

	require.NoError(t, l.Reference(ctx, root, alice, "logo.png", 10))
	require.NoError(t, l.Reference(ctx, root, bob, "logo.png", 20))

	refs, err := l.ReferencesTo(ctx, root)
	require.NoError(t, err)
	require.Len(t, refs, 2)

	// Alice deletes hers. Bob's survives, so the bytes are still referenced.
	require.NoError(t, l.Dereference(ctx, root, alice, "logo.png", 10))
	refs, err = l.ReferencesTo(ctx, root)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, bob.String(), refs[0].Address.String())

	require.NoError(t, l.Dereference(ctx, root, bob, "logo.png", 20))
	refs, err = l.ReferencesTo(ctx, root)
	require.NoError(t, err)
	require.Empty(t, refs)
}

func TestReferencesListsEverythingAndReferencesToNarrows(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	l, _ := newLocal(t, clk)

	r1, r2 := testCID(t, "r1"), testCID(t, "r2")
	a := addr(t)
	require.NoError(t, l.Reference(ctx, r1, a, "one", 1))
	require.NoError(t, l.Reference(ctx, r1, a, "two", 2))
	require.NoError(t, l.Reference(ctx, r2, a, "three", 3))

	all, err := l.References(ctx)
	require.NoError(t, err)
	require.Len(t, all, 3)

	only, err := l.ReferencesTo(ctx, r1)
	require.NoError(t, err)
	require.Len(t, only, 2)

	_, err = l.ReferencesTo(ctx, cid.Undef)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	require.ErrorIs(t, l.Reference(ctx, cid.Undef, a, "x", 1), ErrNotThisServicesRow)
	require.ErrorIs(t, l.Reference(ctx, r1, a, "", 1), ErrEmptyObjectName)
	require.ErrorIs(t, l.Dereference(ctx, r1, Address{}, "x", 1), ErrNoAddress)
}

func TestAnObservationIsLocalAndMonotonic(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	l, _ := newLocal(t, clk)
	a := addr(t)

	for _, observe := range []func(context.Context, Address, string, uint64) (time.Time, error){l.Superseded, l.Absent} {
		clk.set(1_700_000_000_000)
		first, err := observe(ctx, a, "gone.txt", 500)
		require.NoError(t, err)
		require.Equal(t, time.UnixMilli(1_700_000_000_000), first)

		// Finding it again an hour later returns the FIRST observation.
		clk.advance(time.Hour)
		again, err := observe(ctx, a, "gone.txt", 500)
		require.NoError(t, err)
		require.Equal(t, first, again, "first observation wins")

		// Even if the clock goes backwards.
		clk.set(1_600_000_000_000)
		back, err := observe(ctx, a, "gone.txt", 500)
		require.NoError(t, err)
		require.Equal(t, first, back)

		// A different version of the same object is a different observation.
		clk.set(1_700_000_500_000)
		other, err := observe(ctx, a, "gone.txt", 900)
		require.NoError(t, err)
		require.Equal(t, time.UnixMilli(1_700_000_500_000), other)

		_, err = observe(ctx, Address{}, "x", 1)
		require.ErrorIs(t, err, ErrNoAddress)
	}

	superseded, absent, err := l.Observed(ctx, a, "gone.txt", 500)
	require.NoError(t, err)
	require.Equal(t, time.UnixMilli(1_700_000_000_000), superseded)
	require.Equal(t, time.UnixMilli(1_700_000_000_000), absent)

	// Present forgets absence only; Collected forgets both.
	require.NoError(t, l.Present(ctx, a, "gone.txt", 500))
	superseded, absent, err = l.Observed(ctx, a, "gone.txt", 500)
	require.NoError(t, err)
	require.False(t, superseded.IsZero())
	require.True(t, absent.IsZero())
	require.NoError(t, l.Collected(ctx, a, "gone.txt", 500))
	superseded, _, err = l.Observed(ctx, a, "gone.txt", 500)
	require.NoError(t, err)
	require.True(t, superseded.IsZero())

	clk.set(1_700_009_000_000)
	fresh, err := l.Superseded(ctx, a, "gone.txt", 500)
	require.NoError(t, err)
	require.Equal(t, time.UnixMilli(1_700_009_000_000), fresh)

	require.ErrorIs(t, l.Present(ctx, Address{}, "x", 1), ErrNoAddress)
	require.ErrorIs(t, l.Collected(ctx, Address{}, "x", 1), ErrNoAddress)
	_, _, err = l.Observed(ctx, Address{}, "x", 1)
	require.ErrorIs(t, err, ErrNoAddress)
}

func TestACorruptLocalRowIsAnErrorNotAZero(t *testing.T) {
	ctx := context.Background()
	clk := newFixedClock(1_700_000_000_000)
	l, store := newLocal(t, clk)
	a := addr(t)

	for _, namespace := range []string{localSuperseded, localAbsent} {
		key, err := observationKey(namespace, a, "x", 1)
		require.NoError(t, err)
		require.NoError(t, store.Put(ctx, ds.RawKey(key), []byte("nope")))
	}
	_, err := l.Superseded(ctx, a, "x", 1)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	_, _, err = l.Observed(ctx, a, "x", 1)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
	require.NoError(t, l.Present(ctx, a, "x", 1))
	_, _, err = l.Observed(ctx, a, "x", 1)
	require.ErrorIs(t, err, ErrNotThisServicesRow, "the superseded row is still corrupt")

	require.NoError(t, store.Put(ctx, ds.RawKey(localBlocks+"/nope"), []byte("1")))
	_, err = l.IndexedBlocks(ctx)
	require.ErrorIs(t, err, ErrNotThisServicesRow)

	require.NoError(t, store.Put(ctx, ds.RawKey(localRefs+"/a/b"), []byte("1")))
	_, err = l.References(ctx)
	require.Error(t, err)
}

func TestALocalStoreFailurePropagates(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(failingStore{}, time.Now)
	require.NoError(t, err)
	a := addr(t)
	c := testCID(t, "x")

	require.ErrorIs(t, l.Imported(ctx, c), errInjected)
	require.ErrorIs(t, l.Forget(ctx, c), errInjected)
	require.ErrorIs(t, l.Reference(ctx, c, a, "x", 1), errInjected)
	require.ErrorIs(t, l.Dereference(ctx, c, a, "x", 1), errInjected)
	require.ErrorIs(t, l.Present(ctx, a, "x", 1), errInjected)
	require.ErrorIs(t, l.Collected(ctx, a, "x", 1), errInjected)
	_, err = l.Superseded(ctx, a, "x", 1)
	require.ErrorIs(t, err, errInjected)
	_, _, err = l.Observed(ctx, a, "x", 1)
	require.ErrorIs(t, err, errInjected)
	_, err = l.References(ctx)
	require.ErrorIs(t, err, errInjected)
	_, err = l.ReferencesTo(ctx, c)
	require.ErrorIs(t, err, errInjected)
	_, err = l.IndexedBlocks(ctx)
	require.ErrorIs(t, err, errInjected)
}

func TestEncodeAndDecodeMS(t *testing.T) {
	for _, v := range []uint64{0, 1, 1 << 40, 1<<64 - 1} {
		got, err := decodeMS(encodeMS(v))
		require.NoError(t, err)
		require.Equal(t, v, got)
	}
	_, err := decodeMS([]byte("-1"))
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}
