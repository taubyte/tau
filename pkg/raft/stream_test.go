package raft

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
	"gotest.tools/v3/assert"
)

func TestProtocol(t *testing.T) {
	type testCase struct {
		namespace string
		expected  string
	}
	tests := []testCase{
		{"/raft/test", "/tau/raft/v2/raft/test"},
		{"/raft/my-service", "/tau/raft/v2/raft/my-service"},
		{"/raft/complex/path", "/tau/raft/v2/raft/complex/path"},
	}

	for _, tt := range tests {
		result := Protocol(tt.namespace)
		if result != tt.expected {
			t.Errorf("Protocol(%q) = %q, want %q", tt.namespace, result, tt.expected)
		}
	}
}

func TestTransportProtocol(t *testing.T) {
	type testCase struct {
		namespace string
		expected  string
	}
	tests := []testCase{
		{"/raft/test", "/tau/raft/v2/raft/test/transport"},
		{"/raft/my-service", "/tau/raft/v2/raft/my-service/transport"},
		{"/raft/complex/path", "/tau/raft/v2/raft/complex/path/transport"},
	}

	for _, tt := range tests {
		result := TransportProtocol(tt.namespace)
		if result != tt.expected {
			t.Errorf("TransportProtocol(%q) = %q, want %q", tt.namespace, result, tt.expected)
		}
	}
}

func getClusterInternal(c Cluster) *cluster {
	return c.(*cluster)
}

func leaderService(t *testing.T) (Cluster, *raftStreamService) {
	t.Helper()
	cl, err := New(newMockNode(t), "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	t.Cleanup(func() { cl.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	ss := getClusterInternal(cl).current().streamService
	require.NotNil(t, ss, "stream service should be initialized")
	return cl, ss
}

func TestStreamService_HandleGet(t *testing.T) {
	cl, ss := leaderService(t)
	require.NoError(t, cl.Set("testkey", []byte("testvalue"), time.Second))

	resp, err := ss.Get(context.Background(), connect.NewRequest(&raftv2.GetRequest{Key: "testkey"}))
	require.NoError(t, err)
	assert.Assert(t, resp.Msg.GetFound())
	assert.Equal(t, string(resp.Msg.GetValue()), "testvalue")
}

func TestStreamService_HandleGet_NotFound(t *testing.T) {
	_, ss := leaderService(t)

	resp, err := ss.Get(context.Background(), connect.NewRequest(&raftv2.GetRequest{Key: "nonexistent"}))
	require.NoError(t, err)
	assert.Assert(t, !resp.Msg.GetFound())
}

func TestStreamService_HandleGet_MissingKey(t *testing.T) {
	_, ss := leaderService(t)

	_, err := ss.Get(context.Background(), connect.NewRequest(&raftv2.GetRequest{}))
	require.Error(t, err, "expected error for missing key")
}

func TestStreamService_HandleSet_AsLeader(t *testing.T) {
	cl, ss := leaderService(t)

	value := []byte("handlervalue")
	_, err := ss.Set(context.Background(), connect.NewRequest(&raftv2.SetRequest{Key: "handlerkey", Value: value, TimeoutMs: 1000}))
	require.NoError(t, err)

	val, found := cl.Get("handlerkey")
	assert.Assert(t, found)
	assert.Equal(t, string(val), "handlervalue")
}

func TestStreamService_HandleSet_EmptyValue(t *testing.T) {
	cl, ss := leaderService(t)

	_, err := ss.Set(context.Background(), connect.NewRequest(&raftv2.SetRequest{Key: "empty", Value: []byte{}}))
	require.NoError(t, err)

	val, found := cl.Get("empty")
	assert.Assert(t, found)
	assert.Equal(t, len(val), 0)
}

func TestStreamService_HandleSet_MissingKey(t *testing.T) {
	_, ss := leaderService(t)

	_, err := ss.Set(context.Background(), connect.NewRequest(&raftv2.SetRequest{Value: []byte("v")}))
	require.Error(t, err, "expected error for missing key")
}

func TestStreamService_HandleSet_MissingValue(t *testing.T) {
	_, ss := leaderService(t)

	_, err := ss.Set(context.Background(), connect.NewRequest(&raftv2.SetRequest{Key: "k"}))
	require.Error(t, err, "expected error for missing value")
}

func TestStreamService_HandleDelete_AsLeader(t *testing.T) {
	cl, ss := leaderService(t)
	require.NoError(t, cl.Set("delkey", []byte("delvalue"), time.Second))

	_, err := ss.Delete(context.Background(), connect.NewRequest(&raftv2.DeleteRequest{Key: "delkey", TimeoutMs: 1000}))
	require.NoError(t, err)

	_, found := cl.Get("delkey")
	assert.Assert(t, !found)
}

func TestStreamService_HandleDelete_MissingKey(t *testing.T) {
	_, ss := leaderService(t)

	_, err := ss.Delete(context.Background(), connect.NewRequest(&raftv2.DeleteRequest{}))
	require.Error(t, err, "expected error for missing key")
}

func TestStreamService_HandleKeys(t *testing.T) {
	cl, ss := leaderService(t)
	for _, key := range []string{"prefix/a", "prefix/b", "other/c"} {
		require.NoError(t, cl.Set(key, []byte("value"), time.Second))
	}

	resp, err := ss.Keys(context.Background(), connect.NewRequest(&raftv2.KeysRequest{Prefix: "prefix/"}))
	require.NoError(t, err)
	assert.Equal(t, len(resp.Msg.GetKeys()), 2)
}

func TestStreamService_HandleKeys_NoPrefix(t *testing.T) {
	cl, ss := leaderService(t)
	require.NoError(t, cl.Set("key1", []byte("value"), time.Second))

	resp, err := ss.Keys(context.Background(), connect.NewRequest(&raftv2.KeysRequest{}))
	require.NoError(t, err)
	assert.Assert(t, len(resp.Msg.GetKeys()) >= 1)
}

func TestStreamService_HandleClusterInfo(t *testing.T) {
	cl, ss := leaderService(t)

	resp, err := ss.ClusterInfo(context.Background(), connect.NewRequest(&raftv2.ClusterInfoRequest{}))
	require.NoError(t, err)
	leader, err := cl.Leader()
	require.NoError(t, err)
	assert.Equal(t, resp.Msg.GetLeader(), leader.String())
	assert.Equal(t, resp.Msg.GetMemberCount(), int64(1))
}

func TestStreamService_Stop(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	c := getClusterInternal(cl)
	require.NotNil(t, c.current().streamService, "stream service should be initialized")

	c.current().streamService.stop()

	c.current().streamService.stop()

	cl.Close()
}

func TestStreamService_HandleExchangePeers_WithTracker(t *testing.T) {
	cl, ss := leaderService(t)
	c := getClusterInternal(cl)
	require.NotNil(t, c.current().tracker, "peer tracker should be initialized")

	peer1Str := "QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt"

	resp, err := ss.ExchangePeers(context.Background(), connect.NewRequest(&raftv2.ExchangePeersRequest{
		StartMs: time.Now().UnixMilli(),
		SeenAt:  map[string]int64{peer1Str: 10},
	}))
	require.NoError(t, err)
	assert.Assert(t, resp.Msg != nil)

	assert.Equal(t, len(c.current().tracker.peers), 2) // self + peer1
}

func TestStreamService_ForwardToLeader_NoLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Don't wait for leader - a write either applies (already leading) or
	// is refused for want of one; it must not panic either way.
	ss := getClusterInternal(cl).current().streamService
	ss.Set(context.Background(), connect.NewRequest(&raftv2.SetRequest{Key: "test-key", Value: []byte("test-value")}))
}
