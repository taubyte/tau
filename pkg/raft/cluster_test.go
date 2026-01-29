package raft

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	taupeer "github.com/taubyte/tau/p2p/peer"
)

func newMockNode(t *testing.T) taupeer.Node {
	return taupeer.Mock(t.Context())
}

// testTimeoutConfig returns fast timeouts for testing
func testTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    50 * time.Millisecond,
		CommitTimeout:      25 * time.Millisecond,
		LeaderLeaseTimeout: 25 * time.Millisecond,
		SnapshotInterval:   1 * time.Minute,
		SnapshotThreshold:  1000,
	}
}

// testOptions returns common options for unit tests (fast timeouts + short bootstrap)
func testOptions() []Option {
	return []Option{
		WithTimeouts(testTimeoutConfig()),
		WithBootstrapTimeout(100 * time.Millisecond), // Short timeout for fast tests
	}
}

func TestNew_ValidNamespace(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	assert.Equal(t, "test", cluster.Namespace())
}

func TestNew_InvalidNamespace(t *testing.T) {
	node := newMockNode(t)

	tests := []struct {
		name      string
		namespace string
	}{
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(node, tt.namespace)
			assert.Error(t, err, "expected error for invalid namespace")
		})
	}
}

func TestNew_NilNode(t *testing.T) {
	_, err := New(nil, "test")
	assert.Error(t, err, "expected error for nil node")
}

func TestNew_WithOptions(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	assert.Equal(t, "test", cluster.Namespace())
}

func TestCluster_Close(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	// First close should succeed
	assert.NoError(t, cluster.Close(), "first close should succeed")

	// Second close should return ErrAlreadyClosed
	assert.ErrorIs(t, cluster.Close(), ErrAlreadyClosed)
}

func TestCluster_SingleNode_IsLeader(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader election
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	// Single node should be leader
	assert.True(t, cluster.IsLeader(), "single node should be leader")
}

func TestCluster_SingleNode_SetGet(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cluster.Set("testkey", []byte("testvalue"), time.Second), "failed to set")

	val, ok := cluster.Get("testkey")
	assert.True(t, ok, "expected key to exist")
	assert.Equal(t, "testvalue", string(val))
}

func TestCluster_SingleNode_Delete(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cluster.Set("testkey", []byte("testvalue"), time.Second), "failed to set")

	require.NoError(t, cluster.Delete("testkey", time.Second), "failed to delete")

	_, ok := cluster.Get("testkey")
	assert.False(t, ok, "expected key to be deleted")
}

func TestCluster_SingleNode_Keys(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	keys := []string{"config/a", "config/b", "data/x"}
	for _, key := range keys {
		require.NoError(t, cluster.Set(key, []byte("value"), time.Second), "failed to set %s", key)
	}

	allKeys := cluster.Keys("")
	assert.Len(t, allKeys, 3, "expected 3 keys")

	configKeys := cluster.Keys("config/")
	assert.Len(t, configKeys, 2, "expected 2 config keys")
}

func TestCluster_Barrier(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Barrier should succeed
	if err := cluster.Barrier(time.Second); err != nil {
		t.Errorf("barrier failed: %v", err)
	}
}

func TestCluster_Members(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	members, err := cluster.Members()
	if err != nil {
		t.Fatalf("failed to get members: %v", err)
	}

	if len(members) != 1 {
		t.Errorf("expected 1 member, got %d", len(members))
	}
}

func TestCluster_State(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Should be leader state
	state := cluster.State()
	if state.String() != "Leader" {
		t.Errorf("expected Leader state, got %s", state.String())
	}
}

func TestCluster_ClosedOperations(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Operations on closed cluster should fail
	if err := cluster.Set("key", []byte("value"), time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Set, got %v", err)
	}

	if err := cluster.Delete("key", time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Delete, got %v", err)
	}

	if _, err := cluster.Apply([]byte("cmd"), time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Apply, got %v", err)
	}

	if err := cluster.Barrier(time.Second); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Barrier, got %v", err)
	}

	if _, err := cluster.Members(); err != ErrShutdown {
		t.Errorf("expected ErrShutdown for Members, got %v", err)
	}
}

func TestCluster_WaitForLeader_Timeout(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait should succeed for single node
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		t.Fatalf("wait for leader failed: %v", err)
	}
}

func TestCluster_TransferLeadership(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Transfer leadership (will fail for single node but shouldn't panic)
	err = cluster.TransferLeadership()
	// Error is expected for single node cluster
	_ = err
}

func TestCluster_Leader(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Get leader - should be self
	leader, err := cluster.Leader()
	if err != nil {
		t.Fatalf("failed to get leader: %v", err)
	}

	if leader != node.ID() {
		t.Errorf("expected leader to be self, got %s", leader)
	}
}

func TestCluster_Apply(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Apply a set command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "applytest", Value: []byte("applyvalue")},
	}
	data, _ := cbor.Marshal(cmd)

	_, err = cluster.Apply(data, time.Second)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Verify the value was set
	val, ok := cluster.Get("applytest")
	if !ok {
		t.Error("expected key to exist")
	}
	if string(val) != "applyvalue" {
		t.Errorf("expected 'applyvalue', got '%s'", val)
	}
}

func TestCluster_RemoveServer(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Try to remove a non-existent server (should not error)
	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cluster.RemoveServer(dummyPeer, time.Second)
	// Error is acceptable - server doesn't exist
	_ = err
}

func TestCluster_Get_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Get on closed cluster should return not found
	val, ok := cluster.Get("key")
	if ok {
		t.Error("expected not found on closed cluster")
	}
	if val != nil {
		t.Error("expected nil value on closed cluster")
	}
}

func TestCluster_Keys_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Keys on closed cluster should return empty slice
	keys := cluster.Keys("")
	if len(keys) != 0 {
		t.Error("expected empty keys on closed cluster")
	}
}

func TestCluster_IsLeader_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// IsLeader on closed cluster should return false
	if cluster.IsLeader() {
		t.Error("expected not leader on closed cluster")
	}
}

func TestCluster_Leader_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// Leader on closed cluster should return error
	_, err = cluster.Leader()
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_WaitForLeader_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	// WaitForLeader on closed cluster should return error
	ctx := context.Background()
	err = cluster.WaitForLeader(ctx)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_RemoveServer_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cluster.RemoveServer(dummyPeer, time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_TransferLeadership_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.TransferLeadership()
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_State_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	state := cluster.State()
	if state.String() != "Shutdown" {
		t.Errorf("expected Shutdown state, got %s", state.String())
	}
}

func TestCluster_Set_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.Set("key", []byte("value"), time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Delete_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.Delete("key", time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Apply_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	_, err = cluster.Apply([]byte("test"), time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Apply_InvalidCommand(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cluster.WaitForLeader(ctx); err != nil {
		require.NoError(t, err, "failed to wait for leader")
	}

	// Apply invalid data - should succeed but FSM should return error
	resp, err := cluster.Apply([]byte("invalid cbor data"), time.Second)
	if err != nil {
		t.Logf("Apply error: %v", err)
	}
	// The FSM will return an error for invalid command
	if resp.Error != nil {
		t.Logf("FSM error (expected): %v", resp.Error)
	}
}

func TestCluster_Barrier_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	err = cluster.Barrier(time.Second)
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_Members_Closed(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}

	cluster.Close()

	_, err = cluster.Members()
	if err != ErrShutdown {
		t.Errorf("expected ErrShutdown, got %v", err)
	}
}

func TestCluster_RemoveServer_NotLeader(t *testing.T) {
	// For this test, we'd need a follower node, but in single-node test
	// we can at least verify the method exists and doesn't panic
	node := newMockNode(t)

	cluster, err := New(node, "test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Try immediately before becoming leader - should return ErrNotLeader
	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cluster.RemoveServer(dummyPeer, 10*time.Millisecond)
	// Either ErrNotLeader or timeout is acceptable
	_ = err
}

func TestBuildRaftConfig(t *testing.T) {
	node := newMockNode(t)

	cluster, err := New(node, "/raft/test",
		WithTimeouts(TimeoutConfig{
			HeartbeatTimeout:   100 * time.Millisecond,
			ElectionTimeout:    200 * time.Millisecond,
			CommitTimeout:      50 * time.Millisecond,
			LeaderLeaseTimeout: 50 * time.Millisecond,
			SnapshotInterval:   1 * time.Minute,
			SnapshotThreshold:  500,
		}),
	)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cluster.Close()

	// Verify cluster was created successfully
	if cluster.Namespace() != "/raft/test" {
		t.Errorf("expected namespace '/raft/test', got '%s'", cluster.Namespace())
	}
}

func TestCluster_RaftClient(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		require.NoError(t, err, "failed to create cluster")
	}
	defer cl.Close()

	c := getClusterInternal(cl)
	if c.raftClient == nil {
		t.Error("expected raft client to be initialized")
	}
}

func TestCluster_StreamService(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	c := getClusterInternal(cl)
	assert.NotNil(t, c.streamService, "expected stream service to be initialized")
}

func TestCluster_Apply_NotLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Try to apply before becoming leader
	_, err = cl.Apply([]byte("test-command"), 10*time.Millisecond)
	// May fail with ErrNotLeader or timeout - either is acceptable
	_ = err
}

func TestCluster_Apply_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Try to apply an invalid command (will be handled by FSM)
	// The FSM returns an error in the response, but Apply itself may succeed
	resp, err := cl.Apply([]byte("invalid"), 100*time.Millisecond)
	// The apply itself may succeed, but response may contain error
	if err == nil {
		assert.NotNil(t, resp.Error, "FSM should return error for invalid command")
	}
}

func TestCluster_Set_EncodingError(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set should work with any key/value
	err = cl.Set("test-key", []byte("test-value"), 100*time.Millisecond)
	require.NoError(t, err)
}

func TestCluster_Delete_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Delete non-existent key should not error
	err = cl.Delete("nonexistent-key", 100*time.Millisecond)
	require.NoError(t, err)
}

func TestCluster_FSM_Nil_CustomFSM(t *testing.T) {
	node := newMockNode(t)

	// Test that default FSM works
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Default FSM should work
	val, found := cl.Get("test")
	assert.False(t, found)
	assert.Nil(t, val)

	keys := cl.Keys("test")
	assert.Equal(t, 0, len(keys))
}

// mockCustomFSM is a minimal FSM for testing custom FSM path
type mockCustomFSM struct{}

func (m *mockCustomFSM) Apply(log *raft.Log) interface{} {
	return FSMResponse{}
}

func (m *mockCustomFSM) Snapshot() (raft.FSMSnapshot, error) {
	return &mockSnapshot{}, nil
}

func (m *mockCustomFSM) Restore(rc io.ReadCloser) error {
	return nil
}

func (m *mockCustomFSM) Get(key string) ([]byte, bool) {
	return nil, false
}

func (m *mockCustomFSM) Keys(prefix string) []string {
	return []string{}
}

type mockSnapshot struct{}

func (m *mockSnapshot) Persist(sink raft.SnapshotSink) error {
	return sink.Close()
}

func (m *mockSnapshot) Release() {}

func TestCluster_Keys_WithData(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set some keys
	require.NoError(t, cl.Set("prefix-a-1", []byte("value1"), 100*time.Millisecond))
	require.NoError(t, cl.Set("prefix-a-2", []byte("value2"), 100*time.Millisecond))
	require.NoError(t, cl.Set("prefix-b-1", []byte("value3"), 100*time.Millisecond))

	// Get keys with prefix "prefix-a"
	keys := cl.Keys("prefix-a")
	assert.Equal(t, 2, len(keys))

	// Get all keys
	allKeys := cl.Keys("")
	assert.Equal(t, 3, len(allKeys))
}

func TestCluster_Get_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Get non-existent key
	val, found := cl.Get("nonexistent-key")
	assert.False(t, found)
	assert.Nil(t, val)
}

func TestFsmAdapter_Apply_ViaCluster(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/adapter-test")

	// Create a valid set command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "adapter-key", Value: []byte("adapter-value")},
	}
	data, err := cbor.Marshal(cmd)
	require.NoError(t, err)

	log := &raft.Log{Data: data, Index: 1}
	resp := fsm.Apply(log)

	fsmResp, ok := resp.(FSMResponse)
	assert.True(t, ok)
	assert.Nil(t, fsmResp.Error)

	// Verify key was stored
	val, found := fsm.Get("adapter-key")
	assert.True(t, found)
	assert.Equal(t, []byte("adapter-value"), val)
}

func TestFsmAdapter_Snapshot_ViaCluster(t *testing.T) {
	store := newTestStore()
	fsm := newKVFSM(store, "/raft/adapter-test")
	snap, err := fsm.Snapshot()
	assert.NoError(t, err)
	assert.NotNil(t, snap)
}

func TestCluster_AddVoter_NotLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Try to add voter before becoming leader
	dummyPeer, _ := peer.Decode("12D3KooWGzBkj7vvP52A5RRMgQJ4xm4mLe8L1VRyxkj4mHpvT4Yx")
	err = cl.AddVoter(dummyPeer, 10*time.Millisecond)
	// Should fail with ErrNotLeader
	if err != nil {
		assert.Equal(t, ErrNotLeader, err)
	}
}

func TestCluster_Barrier_Success(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Barrier should succeed when leader
	err = cl.Barrier(100 * time.Millisecond)
	assert.NoError(t, err)
}

func TestCluster_TransferLeadership_SingleNode(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// TransferLeadership in single node - should fail (no peer to transfer to)
	err = cl.TransferLeadership()
	// Error is expected for single node
	_ = err
}

func TestCluster_Members_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Get members
	members, err := cl.Members()
	assert.NoError(t, err)
	assert.True(t, len(members) >= 1)
}

func TestCluster_State_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// State should be Leader
	state := cl.State()
	assert.Equal(t, raft.Leader, state)
}

func TestCluster_Leader_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Leader should return our node ID
	leader, err := cl.Leader()
	assert.NoError(t, err)
	assert.Equal(t, node.ID(), leader)
}

func TestCluster_AddVoter_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Try to add self as voter (already a member)
	err = cl.AddVoter(node.ID(), 100*time.Millisecond)
	// Should succeed or return already a member
	assert.NoError(t, err)
}

func TestCluster_Set_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a key
	err = cl.Set("leader-key", []byte("leader-value"), 100*time.Millisecond)
	require.NoError(t, err)

	// Verify it was set
	val, found := cl.Get("leader-key")
	assert.True(t, found)
	assert.Equal(t, []byte("leader-value"), val)
}

func TestCluster_Delete_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a key first
	require.NoError(t, cl.Set("delete-key", []byte("value"), 100*time.Millisecond))

	// Delete it
	err = cl.Delete("delete-key", 100*time.Millisecond)
	require.NoError(t, err)

	// Verify it was deleted
	_, found := cl.Get("delete-key")
	assert.False(t, found)
}

func TestCluster_Close_Idempotent(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	// First close
	err = cl.Close()
	assert.NoError(t, err)

	// Second close should return error
	err = cl.Close()
	assert.Equal(t, ErrAlreadyClosed, err)
}

func TestCluster_Apply_InvalidTimeout(t *testing.T) {
	node := newTestNode(t)

	cluster, err := New(node, "/raft/test-apply-timeout", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cluster.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitForLeader(ctx), "failed to wait for leader")

	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "test", Value: []byte("value")},
	}
	data, _ := cbor.Marshal(cmd)

	// Test with timeout = 0 (should fail)
	_, err = cluster.Apply(data, 0)
	require.Error(t, err, "Apply with timeout=0 should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test with timeout < 0 (should fail)
	_, err = cluster.Apply(data, -1*time.Second)
	require.Error(t, err, "Apply with negative timeout should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test with timeout > MaxApplyTimeout (should fail)
	_, err = cluster.Apply(data, MaxApplyTimeout+time.Second)
	require.Error(t, err, "Apply with timeout > MaxApplyTimeout should fail")
	require.ErrorIs(t, err, ErrInvalidTimeout, "should return ErrInvalidTimeout")

	// Test with timeout = MaxApplyTimeout (should work)
	_, err = cluster.Apply(data, MaxApplyTimeout)
	require.NoError(t, err, "Apply with timeout at MaxApplyTimeout should succeed")

	// Test with valid timeout (1 second, well within limit)
	cmd2 := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "test2", Value: []byte("value2")},
	}
	data2, _ := cbor.Marshal(cmd2)
	_, err = cluster.Apply(data2, time.Second)
	require.NoError(t, err, "Apply with valid timeout should succeed")

	// Verify the value was set
	val, found := cluster.Get("test2")
	require.True(t, found, "key should be found")
	require.Equal(t, []byte("value2"), val, "value should match")
}

func TestCluster_ApplyAsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Apply a valid set command
	cmd := Command{
		Type: CommandSet,
		Set:  &SetCommand{Key: "apply-test", Value: []byte("apply-value")},
	}
	cmdBytes, _ := cbor.Marshal(cmd)

	resp, err := cl.Apply(cmdBytes, time.Second)
	require.NoError(t, err)
	assert.Nil(t, resp.Error)

	// Verify the value was set
	val, found := cl.Get("apply-test")
	assert.True(t, found)
	assert.Equal(t, []byte("apply-value"), val)
}

func TestCluster_SetAndVerify(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set multiple values
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("multi-key-%d", i)
		value := []byte(fmt.Sprintf("multi-value-%d", i))
		require.NoError(t, cl.Set(key, value, time.Second))
	}

	// Verify all values
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("multi-key-%d", i)
		expected := fmt.Sprintf("multi-value-%d", i)
		val, found := cl.Get(key)
		assert.True(t, found)
		assert.Equal(t, expected, string(val))
	}

	// Keys should work
	keys := cl.Keys("multi-")
	assert.Len(t, keys, 5)
}

func TestCluster_DeleteAndVerify(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value
	require.NoError(t, cl.Set("to-delete", []byte("value"), time.Second))

	// Verify it exists
	_, found := cl.Get("to-delete")
	assert.True(t, found)

	// Delete it
	require.NoError(t, cl.Delete("to-delete", time.Second))

	// Verify it's gone
	_, found = cl.Get("to-delete")
	assert.False(t, found)
}

func TestCluster_BarrierSuccess(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Barrier should succeed
	err = cl.Barrier(time.Second)
	assert.NoError(t, err)
}

func TestCluster_LeaderInfo(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Should be leader
	assert.True(t, cl.IsLeader())

	// State should be Leader
	assert.Equal(t, raft.Leader, cl.State())

	// Leader should return our ID
	leader, err := cl.Leader()
	require.NoError(t, err)
	assert.Equal(t, node.ID(), leader)
}

func TestCluster_MembersAfterBootstrap(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Should have exactly one member
	members, err := cl.Members()
	require.NoError(t, err)
	assert.Len(t, members, 1)
	assert.Equal(t, node.ID(), members[0].ID)
	assert.Equal(t, raft.Voter, members[0].Suffrage)
}
