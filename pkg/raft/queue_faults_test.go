package raft

import (
	"errors"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const queueFaultsNamespace = "queue-faults"

// queueOnFaults is a queue on a group of one whose store fails where the test
// says, with the store key of each of the queue's own keys.
func queueOnFaults(t *testing.T, name string) (Queue, Cluster, *faultStore, func(key string) string) {
	t.Helper()
	store := newFaultStore()
	cl, err := New(storeNode{Node: newMockNode(t), store: store}, queueFaultsNamespace,
		WithTimeouts(testTimeoutConfig()), WithForceBootstrap(), WithSnapshotRoot(t.TempDir()))
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

func TestQueue_ACounterThatCannotBeReadAllocatesNothing(t *testing.T) {
	q, _, store, storeKey := queueOnFaults(t, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/_counter"), errDisk)
	require.ErrorIs(t, q.Push("second", []byte("2"), time.Second), errDisk)

	id, data, ok := q.Peek()
	require.True(t, ok)
	require.Equal(t, "first", id, "a push allocated over the item waiting at the head")
	require.Equal(t, []byte("1"), data)
	require.Equal(t, 1, q.Len())
}

func TestQueue_ACorruptCounterAllocatesNothing(t *testing.T) {
	q, cl, _, _ := queueOnFaults(t, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))
	require.NoError(t, cl.Set("_q/jobs/_counter", []byte{0, 1}, time.Second))

	require.ErrorContains(t, q.Push("second", []byte("2"), time.Second), "corrupt queue counter")

	id, _, ok := q.Peek()
	require.True(t, ok)
	require.Equal(t, "first", id)
	require.Equal(t, 1, q.Len())
}

func TestQueue_AnIndexThatCannotBeReadPushesNothing(t *testing.T) {
	q, _, store, storeKey := queueOnFaults(t, "jobs")
	require.NoError(t, q.Push("only", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/idx/only"), errDisk)
	require.ErrorIs(t, q.Push("only", []byte("again"), time.Second), errDisk)
	require.Equal(t, 1, q.Len(), "an id was queued twice")
}

// Peek has no error to give and answers that it has nothing.
func TestQueue_AHeadThatCannotBeReadIsNotAnEmptyQueue(t *testing.T) {
	q, _, store, storeKey := queueOnFaults(t, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/items/"+sequenceID(1)), errDisk)

	_, _, err := q.Pop(time.Second)
	require.ErrorIs(t, err, errDisk)
	require.NotErrorIs(t, err, ErrQueueEmpty)

	_, _, ok := q.Peek()
	require.False(t, ok)
	require.Equal(t, 1, q.Len(), "a pop that could not read its item removed it")
}

func TestQueue_APopThatCannotRemoveItsItemReturnsNothing(t *testing.T) {
	q, _, store, storeKey := queueOnFaults(t, "jobs")
	require.NoError(t, q.Push("first", []byte("1"), time.Second))

	full := errors.New("pebble error during commit: no space left on device")
	store.failPut(storeKey("_q/jobs/items/"+sequenceID(1)), full)

	id, data, err := q.Pop(time.Second)
	require.ErrorIs(t, err, full)
	require.Empty(t, id)
	require.Nil(t, data)

	id, _, ok := q.Peek()
	require.True(t, ok)
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
			q, cl, _, _ := queueOnFaults(t, "jobs")
			require.NoError(t, cl.Set("_q/jobs/items/"+sequenceID(1), tc.raw, time.Second))

			_, _, ok := q.Peek()
			require.False(t, ok)

			_, _, err := q.Pop(time.Second)
			require.ErrorContains(t, err, "corrupt queue entry")
			require.ErrorContains(t, err, tc.says)
			require.Equal(t, 1, q.Len())
		})
	}
}

func TestQueue_NoTimeoutIsTheLongestOne(t *testing.T) {
	q, _, _, _ := queueOnFaults(t, "jobs")

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

	id, data, ok := q.Peek()
	require.False(t, ok)
	require.Empty(t, id)
	require.Nil(t, data)

	require.NoError(t, q.Push("held", []byte("1"), time.Second))
	require.NoError(t, q.Close())
	id, data, ok = q.Peek()
	require.False(t, ok, "a closed queue peeked its head")
	require.Empty(t, id)
	require.Nil(t, data)
}
