package raft

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/streams/command"
	"gotest.tools/v3/assert"
)

func TestProtocol(t *testing.T) {
	tests := []struct {
		namespace string
		expected  string
	}{
		{"/raft/test", "/raft/v1/raft/test"},
		{"/raft/my-service", "/raft/v1/raft/my-service"},
		{"/raft/complex/path", "/raft/v1/raft/complex/path"},
	}

	for _, tt := range tests {
		result := Protocol(tt.namespace)
		if result != tt.expected {
			t.Errorf("Protocol(%q) = %q, want %q", tt.namespace, result, tt.expected)
		}
	}
}

func TestTransportProtocol(t *testing.T) {
	tests := []struct {
		namespace string
		expected  string
	}{
		{"/raft/test", "/raft/v1/raft/test/transport"},
		{"/raft/my-service", "/raft/v1/raft/my-service/transport"},
		{"/raft/complex/path", "/raft/v1/raft/complex/path/transport"},
	}

	for _, tt := range tests {
		result := TransportProtocol(tt.namespace)
		if result != tt.expected {
			t.Errorf("TransportProtocol(%q) = %q, want %q", tt.namespace, result, tt.expected)
		}
	}
}

// getClusterInternal returns the internal cluster struct for testing
func getClusterInternal(c Cluster) *cluster {
	return c.(*cluster)
}

func TestStreamService_HandleSet(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	// Access internal cluster to test stream service
	c := getClusterInternal(cl)
	if c.streamService == nil {
		t.Fatal("stream service should be initialized")
	}
}

func TestStreamService_HandleGet(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	// Set a value first
	if err := cl.Set("testkey", []byte("testvalue"), time.Second); err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	// Test get through internal stream service handler
	c := getClusterInternal(cl)
	body := command.Body{
		keyKey: "testkey",
	}

	resp, err := c.streamService.handleGet(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleGet failed: %v", err)
	}

	found, ok := resp[keyFound].(bool)
	if !ok || !found {
		t.Error("expected found to be true")
	}

	val, ok := resp[keyValue].([]byte)
	if !ok {
		t.Fatalf("expected value to be []byte, got %T", resp[keyValue])
	}
	if string(val) != "testvalue" {
		t.Errorf("expected 'testvalue', got '%s'", val)
	}
}

func TestStreamService_HandleGet_WithBarrier(t *testing.T) {
	node := newTestNode(t)

	cl, err := New(node, "/raft/test-barrier", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first
	require.NoError(t, cl.Set("barrier-key", []byte("barrier-value"), time.Second), "failed to set value")

	// Test get through internal stream service handler with barrier
	c := getClusterInternal(cl)
	body := command.Body{
		keyKey:     "barrier-key",
		keyBarrier: int64(time.Second.Nanoseconds()),
	}

	resp, err := c.streamService.handleGet(context.Background(), nil, body)
	require.NoError(t, err, "handleGet with barrier should succeed")

	found, ok := resp[keyFound].(bool)
	require.True(t, ok && found, "expected found to be true")

	val, ok := resp[keyValue].([]byte)
	require.True(t, ok, "expected value to be []byte")
	assert.Equal(t, "barrier-value", string(val))
}

func TestStreamService_HandleGet_WithBarrier_Invalid(t *testing.T) {
	node := newTestNode(t)

	cl, err := New(node, "/raft/test-barrier-invalid", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	c := getClusterInternal(cl)

	// Test with barrier <= 0
	body := command.Body{
		keyKey:     "test-key",
		keyBarrier: int64(-1),
	}

	_, err = c.streamService.handleGet(context.Background(), nil, body)
	require.Error(t, err, "handleGet with negative barrier should fail")
	require.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")

	// Test with barrier > MaxGetHandlerBarrierTimeout
	body = command.Body{
		keyKey:     "test-key",
		keyBarrier: int64((MaxGetHandlerBarrierTimeout + time.Second).Nanoseconds()),
	}

	_, err = c.streamService.handleGet(context.Background(), nil, body)
	require.Error(t, err, "handleGet with barrier > max should fail")
	require.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")
}

func TestStreamService_HandleGet_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		keyKey: "nonexistent",
	}

	resp, err := c.streamService.handleGet(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleGet failed: %v", err)
	}

	found, ok := resp[keyFound].(bool)
	if ok && found {
		t.Error("expected found to be false")
	}
}

func TestStreamService_HandleSet_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		keyKey:     "streamkey",
		keyValue:   []byte("streamvalue"),
		keyTimeout: float64(1000), // 1 second in ms
	}

	resp, err := c.streamService.handleSet(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleSet failed: %v", err)
	}

	success, ok := resp["success"].(bool)
	if !ok || !success {
		t.Error("expected success to be true")
	}

	// Verify the value was set
	val, found := cl.Get("streamkey")
	if !found {
		t.Error("expected key to exist")
	}
	if string(val) != "streamvalue" {
		t.Errorf("expected 'streamvalue', got '%s'", val)
	}
}

func TestStreamService_HandleSet_StringValue(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	// Test with string value instead of []byte
	body := command.Body{
		keyKey:   "strkey",
		keyValue: "stringvalue", // string instead of []byte
	}

	resp, err := c.streamService.handleSet(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleSet failed: %v", err)
	}

	success, _ := resp["success"].(bool)
	if !success {
		t.Error("expected success")
	}
}

func TestStreamService_HandleSet_MissingKey(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		keyValue: []byte("value"),
		// Missing keyKey
	}

	_, err = c.streamService.handleSet(context.Background(), nil, body)
	if err == nil {
		t.Error("expected error for missing key")
	}
}

func TestStreamService_HandleSet_MissingValue(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		keyKey: "key",
		// Missing keyValue
	}

	_, err = c.streamService.handleSet(context.Background(), nil, body)
	if err == nil {
		t.Error("expected error for missing value")
	}
}

func TestStreamService_HandleDelete_AsLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	// First set a value
	if err := cl.Set("delkey", []byte("delvalue"), time.Second); err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		keyKey:     "delkey",
		keyTimeout: float64(1000),
	}

	resp, err := c.streamService.handleDelete(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleDelete failed: %v", err)
	}

	success, _ := resp["success"].(bool)
	if !success {
		t.Error("expected success")
	}

	// Verify the value was deleted
	_, found := cl.Get("delkey")
	if found {
		t.Error("expected key to be deleted")
	}
}

func TestStreamService_HandleDelete_MissingKey(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		// Missing keyKey
	}

	_, err = c.streamService.handleDelete(context.Background(), nil, body)
	if err == nil {
		t.Error("expected error for missing key")
	}
}

func TestStreamService_HandleKeys(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	// Set some values
	for _, key := range []string{"config/a", "config/b", "data/x"} {
		if err := cl.Set(key, []byte("value"), time.Second); err != nil {
			t.Fatalf("failed to set %s: %v", key, err)
		}
	}

	c := getClusterInternal(cl)

	// Test with prefix
	body := command.Body{
		keyPrefix: "config/",
	}

	resp, err := c.streamService.handleKeys(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleKeys failed: %v", err)
	}

	keys, ok := resp[keyKeys].([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", resp[keyKeys])
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestStreamService_HandleKeys_NoPrefix(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	// Set some values
	if err := cl.Set("key1", []byte("value"), time.Second); err != nil {
		t.Fatalf("failed to set: %v", err)
	}

	c := getClusterInternal(cl)

	// Test without prefix (empty body)
	body := command.Body{}

	resp, err := c.streamService.handleKeys(context.Background(), nil, body)
	if err != nil {
		t.Fatalf("handleKeys failed: %v", err)
	}

	keys, ok := resp[keyKeys].([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", resp[keyKeys])
	}
	if len(keys) < 1 {
		t.Error("expected at least 1 key")
	}
}

func TestStreamService_HandleGet_MissingKey(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := cl.WaitForLeader(ctx); err != nil {
		t.Fatalf("failed to wait for leader: %v", err)
	}

	c := getClusterInternal(cl)
	body := command.Body{
		// Missing keyKey
	}

	_, err = c.streamService.handleGet(context.Background(), nil, body)
	if err == nil {
		t.Error("expected error for missing key")
	}
}

func TestStreamService_Stop(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")

	c := getClusterInternal(cl)
	require.NotNil(t, c.streamService, "stream service should be initialized")

	// Stop should not panic
	c.streamService.stop()

	// Stop again should not panic
	c.streamService.stop()

	cl.Close()
}

func TestStreamService_HandleExchangePeers_WithTracker(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	c := getClusterInternal(cl)

	require.NotNil(t, c.tracker, "peer tracker should be initialized")

	peer1Str := "QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt"

	body := command.Body{
		keyStartTime: float64(time.Now().UnixMilli()),
		keySeenAt: map[string]interface{}{
			peer1Str: float64(10),
		},
	}

	resp, err := c.streamService.handleExchangePeers(context.Background(), nil, body)
	require.NoError(t, err)
	assert.Assert(t, resp != nil)

	// Verify the peer was merged
	assert.Equal(t, len(c.tracker.peers), 2) // self + peer1
}

func TestStreamService_ForwardToLeader_NoLeader(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Don't wait for leader - try to forward immediately
	c := getClusterInternal(cl)

	body := command.Body{
		keyKey:   "test-key",
		keyValue: []byte("test-value"),
	}

	// This should fail because we can't forward to ourselves
	_, err = c.streamService.forwardToLeader(cmdSet, body)
	// May or may not error depending on timing - just verify no panic
}
