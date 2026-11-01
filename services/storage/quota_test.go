package storage

import (
	"context"
	"testing"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
)

func TestUsageIsASumOverPerWriterRows(t *testing.T) {
	eachBackend(t, func(t *testing.T, store Datastore) {
		ctx := context.Background()
		i := index{store: store}
		a := addr(t)

		u, err := i.Usage(ctx, a)
		require.NoError(t, err)
		require.Zero(t, u.Used())
		require.Zero(t, u.Writers)

		require.NoError(t, i.Add(ctx, a, "peerA", 100))
		require.NoError(t, i.Add(ctx, a, "peerA", 50))
		require.NoError(t, i.Add(ctx, a, "peerB", 25))
		require.NoError(t, i.Remove(ctx, a, "peerB", 5))

		u, err = i.Usage(ctx, a)
		require.NoError(t, err)
		require.Equal(t, uint64(175), u.Added)
		require.Equal(t, uint64(5), u.Removed)
		require.Equal(t, uint64(170), u.Used())
		require.Equal(t, 2, u.Writers, "the overshoot multiplier is reported, not inferred")
	})
}

func TestConcurrentWritersDoNotLoseEachOthersAccounting(t *testing.T) {
	ctx := context.Background()
	a := addr(t)

	// Two nodes, each with its own view, each writing only its own row. Merging
	// the two stores is a union of keys, which is what the CRDT does.
	merged := newOrderedMem()
	for _, w := range []string{"peerA", "peerB", "peerC"} {
		own := newOrderedMem()
		i := index{store: own}
		require.NoError(t, i.Add(ctx, a, w, 100))
		require.NoError(t, i.Remove(ctx, a, w, 10))

		for k, v := range own.rows {
			require.NoError(t, merged.Put(ctx, ds.RawKey(k), v))
		}
	}

	u, err := index{store: merged}.Usage(ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint64(300), u.Added)
	require.Equal(t, uint64(30), u.Removed)
	require.Equal(t, uint64(270), u.Used())
	require.Equal(t, 3, u.Writers)
}

func TestUsedSaturatesRatherThanUnderflows(t *testing.T) {
	// A removed sum ahead of an added sum is a partial merge, which is a
	// moment in time and not a negative object.
	require.Zero(t, Usage{Added: 5, Removed: 9}.Used())
	require.Zero(t, Usage{Added: 0, Removed: 0}.Used())
	require.Equal(t, uint64(4), Usage{Added: 9, Removed: 5}.Used())
}

func TestTheCheckpointIsNotATermInTheSum(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	a := addr(t)

	require.NoError(t, i.Add(ctx, a, "peerA", 100))
	require.NoError(t, i.Checkpoint(ctx, a, "peerA", 100))

	u, err := i.Usage(ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint64(100), u.Used(), "adding the checkpoint would double-count what it summarises")
}

func TestAdmissionIsCheckedAgainstTheSum(t *testing.T) {
	ctx := context.Background()
	i := index{store: newOrderedMem()}
	a := addr(t)

	require.NoError(t, i.Add(ctx, a, "peerA", 900))

	ok, u, err := i.Admit(ctx, a, 1000, 50)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(900), u.Used())

	ok, _, err = i.Admit(ctx, a, 1000, 100)
	require.NoError(t, err)
	require.True(t, ok, "exactly at the ceiling is admitted")

	ok, _, err = i.Admit(ctx, a, 1000, 101)
	require.NoError(t, err)
	require.False(t, ok)

	// A capacity of zero is the vocabulary's own convention for "no ceiling".
	ok, _, err = i.Admit(ctx, a, 0, 1<<40)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestTwoCoordinatorsCanOvershootByOneObjectEach(t *testing.T) {
	ctx := context.Background()
	a := addr(t)
	const capacity, object = 1000, 400

	// Both read 800 and both admit a 400-byte object, because neither has seen
	// the other's write yet.
	shared := newOrderedMem()
	i := index{store: shared}
	require.NoError(t, i.Add(ctx, a, "peerA", 800))

	okA, _, err := i.Admit(ctx, a, capacity, object)
	require.NoError(t, err)
	okB, _, err := i.Admit(ctx, a, capacity, object)
	require.NoError(t, err)
	require.False(t, okA)
	require.False(t, okB)

	// At 500 they both admit, and the result is 1300 against a ceiling of
	// 1000: an overshoot of exactly (coordinators) x (object), and no more.
	shared2 := newOrderedMem()
	j := index{store: shared2}
	require.NoError(t, j.Add(ctx, a, "peerA", 500))
	okA, _, err = j.Admit(ctx, a, capacity, object)
	require.NoError(t, err)
	require.True(t, okA)
	okB, _, err = j.Admit(ctx, a, capacity, object)
	require.NoError(t, err)
	require.True(t, okB)

	require.NoError(t, j.Add(ctx, a, "peerA", object))
	require.NoError(t, j.Add(ctx, a, "peerB", object))
	u, err := j.Usage(ctx, a)
	require.NoError(t, err)
	require.Equal(t, uint64(1300), u.Used())
	require.Equal(t, uint64(capacity+2*object-500), u.Used()+0, "the bound is coordinators x object size")

	// And the ceiling holds from there: nothing more is admitted.
	ok, _, err := j.Admit(ctx, a, capacity, 1)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestUsageRefusesRowsItDidNotWrite(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	prefix, err := a.usagePrefix()
	require.NoError(t, err)

	require.NoError(t, store.Put(ctx, ds.RawKey(prefix+"/"+segment("peerA")+"/nonsense"), []byte("1")))
	_, err = i.Usage(ctx, a)
	require.ErrorIs(t, err, ErrNotThisServicesRow)

	store2 := newOrderedMem()
	require.NoError(t, store2.Put(ctx, ds.RawKey(prefix+"/tooshallow"), []byte("1")))
	_, err = index{store: store2}.Usage(ctx, a)
	require.ErrorIs(t, err, ErrNotThisServicesRow)

	store3 := newOrderedMem()
	require.NoError(t, store3.Put(ctx, ds.RawKey(prefix+"/"+segment("peerA")+"/"+usageAdded), []byte("not a number")))
	_, err = index{store: store3}.Usage(ctx, a)
	require.ErrorIs(t, err, ErrNotThisServicesRow)
}

func TestQuotaDoorsRefuseABadAddressAndPropagateFailures(t *testing.T) {
	ctx := context.Background()
	var zero Address
	i := index{store: newOrderedMem()}

	require.ErrorIs(t, i.Add(ctx, zero, "w", 1), ErrNoAddress)
	require.ErrorIs(t, i.Remove(ctx, zero, "w", 1), ErrNoAddress)
	require.ErrorIs(t, i.Checkpoint(ctx, zero, "w", 1), ErrNoAddress)
	_, err := i.Usage(ctx, zero)
	require.ErrorIs(t, err, ErrNoAddress)
	_, _, err = i.Admit(ctx, zero, 1, 1)
	require.ErrorIs(t, err, ErrNoAddress)

	a := addr(t)
	f := index{store: failingStore{}}
	require.ErrorIs(t, f.Add(ctx, a, "w", 1), errInjected)
	require.ErrorIs(t, f.Checkpoint(ctx, a, "w", 1), errInjected)
	_, err = f.Usage(ctx, a)
	require.ErrorIs(t, err, errInjected)
}

func TestACounterRefusesToOverflow(t *testing.T) {
	ctx := context.Background()
	store := newOrderedMem()
	i := index{store: store}
	a := addr(t)

	key, err := a.usageKey("peerA", usageAdded)
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, ds.RawKey(key), encodeMS(1<<64-1)))

	err = i.Add(ctx, a, "peerA", 1)
	require.ErrorContains(t, err, "overflows")
}
