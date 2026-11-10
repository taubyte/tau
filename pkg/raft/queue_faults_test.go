package raft

import (
	"errors"
	"path"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
)

const queueFaultsNamespace = "queue-faults"

// queueOnFaults is a queue on a group of one whose store fails where the test
// says, with the store key of each of the queue's own keys.
func queueOnFaults(t *testing.T, mn *taupeer.Mocknet, name string) (Queue, Cluster, *faultStore, func(key string) string) {
	t.Helper()
	store := newFaultStore()
	cl, err := New(storeNode{Node: newMockNode(t, mn), store: store}, queueFaultsNamespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap())
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	waitLeader(t, cl, 5*time.Second)

	q := NewQueue(cl, name)
	t.Cleanup(func() { q.Close() })

	storeKey := func(key string) string {
		return path.Join(RaftStoragePrefix, queueFaultsNamespace, "data", key)
	}
	return q, cl, store, storeKey
}

var errDisk = errors.New("pebble error during get: input/output error")

// lenOf is q's length, failing t when it cannot be read.
func lenOf(t testing.TB, q Queue) int {
	t.Helper()
	n, err := q.Len()
	require.NoError(t, err)
	return n
}

// keyLister is what lists a group's keys: its cluster, its state machine, a mock.
type keyLister interface {
	Keys(prefix string) ([]string, error)
}

// keysOf is l's keys under prefix, failing t when they cannot be listed.
func keysOf(t testing.TB, l keyLister, prefix string) []string {
	t.Helper()
	keys, err := l.Keys(prefix)
	require.NoError(t, err)
	return keys
}

func TestQueue_ACounterThatCannotBeReadAllocatesNothing(t *testing.T) {
	mn := peertest.Net(t)
	q, _, store, storeKey := queueOnFaults(t, mn, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/_counter"), errDisk)
	require.ErrorIs(t, q.Push("second", []byte("2"), time.Second), errDisk)

	id, data, err := q.Peek()
	require.NoError(t, err)
	require.Equal(t, "first", id, "a push allocated over the item waiting at the head")
	require.Equal(t, []byte("1"), data)
	require.Equal(t, 1, lenOf(t, q))
}

func TestQueue_ACorruptCounterAllocatesNothing(t *testing.T) {
	mn := peertest.Net(t)
	q, cl, _, _ := queueOnFaults(t, mn, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))
	require.NoError(t, cl.Set("_q/jobs/_counter", []byte{0, 1}, time.Second))

	require.ErrorContains(t, q.Push("second", []byte("2"), time.Second), "corrupt queue counter")

	id, _, err := q.Peek()
	require.NoError(t, err)
	require.Equal(t, "first", id)
	require.Equal(t, 1, lenOf(t, q))
}

func TestQueue_AnIndexThatCannotBeReadPushesNothing(t *testing.T) {
	mn := peertest.Net(t)
	q, _, store, storeKey := queueOnFaults(t, mn, "jobs")
	require.NoError(t, q.Push("only", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/idx/only"), errDisk)
	require.ErrorIs(t, q.Push("only", []byte("again"), time.Second), errDisk)
	require.Equal(t, 1, lenOf(t, q), "an id was queued twice")
}

func TestQueue_AHeadThatCannotBeReadIsNotAnEmptyQueue(t *testing.T) {
	mn := peertest.Net(t)
	q, _, store, storeKey := queueOnFaults(t, mn, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/items/"+sequenceID(1)), errDisk)

	_, _, err := q.Pop(time.Second)
	require.ErrorIs(t, err, errDisk)
	require.NotErrorIs(t, err, ErrQueueEmpty)

	_, _, err = q.Peek()
	require.ErrorIs(t, err, errDisk)
	require.NotErrorIs(t, err, ErrQueueEmpty)
	require.Equal(t, 1, lenOf(t, q), "a pop that could not read its item removed it")
}

func TestQueue_APopThatCannotRemoveItsItemReturnsNothing(t *testing.T) {
	mn := peertest.Net(t)
	q, _, store, storeKey := queueOnFaults(t, mn, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))

	full := errors.New("pebble error during commit: no space left on device")
	store.failPut(storeKey("_q/jobs/items/"+sequenceID(1)), full)

	id, data, err := q.Pop(time.Second)
	require.ErrorIs(t, err, full)
	require.Empty(t, id)
	require.Nil(t, data)

	id, _, err = q.Peek()
	require.NoError(t, err)
	require.Equal(t, "first", id)
}

// An entry is a length, an id of that length, then data.
func TestQueue_ACorruptEntryIsNeitherPoppedNorPeeked(t *testing.T) {
	type testCase struct {
		name string
		raw  []byte
		says string
	}
	for _, tc := range []testCase{
		{"too short for its length", []byte{0, 0}, "entry too short"},
		{"shorter than its id", []byte{0, 0, 0, 9, 'a', 'b'}, "entry truncated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mn := peertest.Net(t)
			q, cl, _, _ := queueOnFaults(t, mn, "jobs")
			require.NoError(t, cl.Set("_q/jobs/items/"+sequenceID(1), tc.raw, time.Second))

			_, _, err := q.Peek()
			require.ErrorContains(t, err, "corrupt queue entry")
			require.ErrorContains(t, err, tc.says)

			_, _, err = q.Pop(time.Second)
			require.ErrorContains(t, err, "corrupt queue entry")
			require.ErrorContains(t, err, tc.says)
			require.Equal(t, 1, lenOf(t, q))
		})
	}
}

func TestQueue_NoTimeoutIsTheLongestOne(t *testing.T) {
	mn := peertest.Net(t)
	q, _, _, _ := queueOnFaults(t, mn, "jobs")

	require.NoError(t, q.Push("zero", []byte("0"), 0))
	require.NoError(t, q.Push("negative", []byte("-"), -time.Second))

	id, _, err := q.Pop(0)
	require.NoError(t, err)
	require.Equal(t, "zero", id)
	id, _, err = q.Pop(-time.Second)
	require.NoError(t, err)
	require.Equal(t, "negative", id)
}

func TestQueue_PeekSeesNothingInAnEmptyOrClosedQueue(t *testing.T) {
	q := NewQueue(NewMockCluster(), "peek")

	id, data, err := q.Peek()
	require.ErrorIs(t, err, ErrQueueEmpty)
	require.Empty(t, id)
	require.Nil(t, data)

	require.NoError(t, q.Push("held", []byte("1"), time.Second))
	require.NoError(t, q.Close())
	id, data, err = q.Peek()
	require.ErrorIs(t, err, ErrShutdown, "a closed queue peeked its head")
	require.Empty(t, id)
	require.Nil(t, data)
}

// A KEY LISTING THE STORE FAILS PARTWAY, OR ONE HOLDING AN ENTRY THAT DOES NOT
// DECODE, IS AN ERROR: a queue would otherwise read it as empty, and a bound
// counted from it would let everything through.
func TestQueue_AKeyListingTheStoreFailsIsAnError(t *testing.T) {
	mn := peertest.Net(t)
	q, cl, store, storeKey := queueOnFaults(t, mn, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))
	require.NotEmpty(t, keysOf(t, cl, "_q/jobs/"))

	store.failScan(path.Join(RaftStoragePrefix, queueFaultsNamespace, "data"), errDisk)
	_, err := cl.Keys("")
	require.ErrorIs(t, err, errDisk)
	_, _, err = q.Pop(time.Second)
	require.ErrorIs(t, err, errDisk)
	require.NotErrorIs(t, err, ErrQueueEmpty)
	_, err = q.Len()
	require.ErrorIs(t, err, errDisk)

	store.failScan(path.Join(RaftStoragePrefix, queueFaultsNamespace, "data"), nil)
	require.NoError(t, store.Put(t.Context(), ds.NewKey(storeKey("_q/jobs/garbled")), []byte{0xff}))
	_, err = cl.Keys("_q/")
	require.ErrorContains(t, err, "garbled")
	require.Empty(t, keysOf(t, cl, "elsewhere/"), "an entry under another prefix failed this one")
}
