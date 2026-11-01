package raft

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"github.com/stretchr/testify/require"
)

var _ Cluster = (*MockCluster)(nil)

func TestMockCluster_HoldsWhatItIsGiven(t *testing.T) {
	m := NewMockCluster()

	value := []byte("one")
	require.NoError(t, m.Set("a/1", value, time.Second))
	value[0] = 'X'

	got, found := m.Get("a/1")
	require.True(t, found)
	require.Equal(t, []byte("one"), got, "a caller reusing its buffer rewrote what the mock holds")

	got, found, err := m.TryGet("a/1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("one"), got)

	got, found, err = m.TryGet("a/absent")
	require.NoError(t, err, "the mock has no failing read")
	require.False(t, found)
	require.Nil(t, got)

	require.NoError(t, m.Set("a/2", []byte("two"), time.Second))
	require.NoError(t, m.Set("b/1", []byte("three"), time.Second))
	keys := m.Keys("a/")
	sort.Strings(keys)
	require.Equal(t, []string{"a/1", "a/2"}, keys)

	require.NoError(t, m.Delete("a/1", time.Second))
	require.NoError(t, m.Delete("a/never", time.Second), "deleting what is not there succeeds")
	_, found = m.Get("a/1")
	require.False(t, found)
	require.Equal(t, []string{"a/2"}, m.Keys("a/"))

	batched := []byte("four")
	require.NoError(t, m.Batch([]BatchOp{
		{Set: &SetCommand{Key: "c/1", Value: batched}},
		{Delete: &DeleteCommand{Key: "a/2"}},
	}, time.Second))
	batched[0] = 'X'
	got, found = m.Get("c/1")
	require.True(t, found)
	require.Equal(t, []byte("four"), got)
	_, found = m.Get("a/2")
	require.False(t, found)
}

func TestMockCluster_IsALeaderThatNeverRefuses(t *testing.T) {
	m := NewMockCluster()

	require.Equal(t, "test", m.Namespace())
	require.True(t, m.IsLeader())
	require.Equal(t, raft.Leader, m.State())
	require.NoError(t, m.Barrier(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, m.WaitForLeader(ctx), "the mock leads already, so even a done context finds its leader")

	leader, err := m.Leader()
	require.NoError(t, err)
	require.Empty(t, leader)

	members, err := m.Members()
	require.NoError(t, err)
	require.Empty(t, members)

	resp, err := m.Apply([]byte("anything"), time.Second)
	require.NoError(t, err)
	require.Equal(t, FSMResponse{}, resp)

	require.NoError(t, m.AddVoter("someone", time.Second))
	require.NoError(t, m.RemoveServer("someone", time.Second))
	require.NoError(t, m.TransferLeadership())
	require.True(t, m.IsLeader(), "transferring leadership moves nothing on a group of one")

	require.NoError(t, m.Close())
	require.NoError(t, m.Set("after", []byte("close"), time.Second), "Close releases nothing the mock holds")
	got, found := m.Get("after")
	require.True(t, found)
	require.Equal(t, []byte("close"), got)
}
