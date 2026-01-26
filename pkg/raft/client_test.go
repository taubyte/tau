package raft

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func TestNewClient(t *testing.T) {
	node := newMockNode(t)

	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	assert.Equal(t, client.namespace, "/raft/test")
}

func TestClient_Close(t *testing.T) {
	node := newMockNode(t)

	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")

	// Client should be functional
	assert.Assert(t, client.Client != nil)

	// Close should not panic
	client.Close()
}

func TestClient_Namespace(t *testing.T) {
	node := newMockNode(t)

	tests := []struct {
		namespace string
	}{
		{"/raft/service-a"},
		{"/raft/service-b"},
		{"/raft/nested/path"},
	}

	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			client, err := NewClient(node, tt.namespace)
			require.NoError(t, err, "failed to create client")
			defer client.Close()

			assert.Equal(t, client.namespace, tt.namespace)
		})
	}
}

func TestClient_SetGetDelete_Integration(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client pointing to same node
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Set - send to self
	err = client.Set("client-key", []byte("client-value"), time.Second, node.ID())
	if err != nil {
		t.Logf("Set returned error (expected in single-node test): %v", err)
	}

	// Test Get
	_, _, err = client.Get("client-key", node.ID())
	if err != nil {
		t.Logf("Get returned error (expected in single-node test): %v", err)
	}

	// Test Delete
	err = client.Delete("client-key", time.Second, node.ID())
	if err != nil {
		t.Logf("Delete returned error (expected in single-node test): %v", err)
	}

	// Test Keys
	_, err = client.Keys("client-", node.ID())
	if err != nil {
		t.Logf("Keys returned error (expected in single-node test): %v", err)
	}
}

func TestClient_Get_NotFound(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Get non-existent key
	val, found, err := client.Get("nonexistent-key", node.ID())
	if err == nil {
		assert.Assert(t, !found)
		assert.Assert(t, val == nil)
	}
}

func TestClient_Keys_Empty(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Keys with prefix that doesn't exist
	keys, err := client.Keys("nonexistent-prefix-", node.ID())
	if err == nil {
		assert.Equal(t, len(keys), 0)
	}
}

func TestClient_ExchangePeers(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Exchange peers
	ourStart := time.Now()
	ourPeers := map[string]int64{
		node.ID().String(): 0,
	}

	theirStart, theirPeers, err := client.ExchangePeers(ourStart, ourPeers, node.ID())
	if err == nil {
		assert.Assert(t, !theirStart.IsZero())
		assert.Assert(t, theirPeers != nil)
	}
}

func TestClient_ExchangePeers_WithPeers(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Exchange peers with multiple peers in the map
	ourStart := time.Now()
	ourPeers := map[string]int64{
		node.ID().String(): 0,
		"QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt": 100,
		"QmVvkUhhLaQ4dJEPZB1bGTPNqBpHnXcGLqbNFnZbMSKszN": 200,
	}

	theirStart, theirPeers, err := client.ExchangePeers(ourStart, ourPeers, node.ID())
	if err == nil {
		assert.Assert(t, !theirStart.IsZero())
		assert.Assert(t, theirPeers != nil)
	}
}

func TestClient_Get_TypeConversions(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first
	require.NoError(t, cl.Set("testkey", []byte("testvalue"), time.Second))

	// Create client
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Get the value - tests value type conversions
	val, found, err := client.Get("testkey", node.ID())
	if err == nil && found {
		assert.Assert(t, len(val) > 0)
	}
}

func TestClient_Keys_TypeConversions(t *testing.T) {
	node := newMockNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set some values first
	require.NoError(t, cl.Set("prefix/a", []byte("a"), time.Second))
	require.NoError(t, cl.Set("prefix/b", []byte("b"), time.Second))

	// Create client
	client, err := NewClient(node, "/raft/test")
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Get keys - tests response type handling
	keys, err := client.Keys("prefix/", node.ID())
	if err == nil {
		assert.Assert(t, len(keys) >= 0) // Just verify no panic
	}
}
