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

// TestQueue_ACounterThatCannotBeReadAllocatesNothing: Push takes the next
// position from the counter. Read through Get, a counter the store could not
// read was a counter of zero, and the push landed on position one — over the
// item still waiting there.
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

// TestQueue_ACorruptCounterAllocatesNothing: a counter is always eight bytes,
// so one of any other length is damage, and reading it as zero allocates over
// what the queue holds just the same.
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

// TestQueue_AnIndexThatCannotBeReadPushesNothing: the index is how Push knows
// an id is already queued, and an index read that failed read as "not queued"
// queued it a second time.
func TestQueue_AnIndexThatCannotBeReadPushesNothing(t *testing.T) {
	q, _, store, storeKey := queueOnFaults(t, "jobs")
	require.NoError(t, q.Push("only", []byte("1"), time.Second))

	store.failGet(storeKey("_q/jobs/idx/only"), errDisk)
	require.ErrorIs(t, q.Push("only", []byte("again"), time.Second), errDisk)
	require.Equal(t, 1, q.Len(), "an id was queued twice")
}

// TestQueue_AHeadThatCannotBeReadIsNotAnEmptyQueue: Pop says why it returned
// nothing. A queue whose head the store could not read is not empty, and a
// consumer told it was would stop draining it. Peek has no error to give and
// answers that it has nothing.
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

// TestQueue_APopThatCannotRemoveItsItemReturnsNothing: an item handed to a
// consumer while it stays queued is handed out twice.
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

// TestQueue_ACorruptEntryIsNeitherPoppedNorPeeked: an entry is a length, an id
// of that length, then data. One too short to hold its length, or shorter than
// the id it announces, is refused by Pop and stays where it is.
func TestQueue_ACorruptEntryIsNeitherPoppedNorPeeked(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
		says string
	}{
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

// TestQueue_NoTimeoutIsTheLongestOne: a zero or negative timeout is the
// longest an apply may take, not one that has already run out.
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

// TestQueue_PeekSeesNothingInAnEmptyOrClosedQueue: Peek reports the head, so
// an empty queue and a closed one report none.
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
