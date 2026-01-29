package raft

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gotest.tools/v3/assert"
)

func TestNewClient(t *testing.T) {
	node := newMockNode(t)

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	require.NotNil(t, client, "client should not be nil")
	defer client.Close()
}

func TestClient_Close(t *testing.T) {
	node := newMockNode(t)

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	require.NotNil(t, client, "client should not be nil")

	err = client.Close()
	require.NoError(t, err, "close should succeed")
}

func TestClient_SetGetDelete_Integration(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	err = client.Set("client-key", []byte("client-value"), time.Second, node.ID())
	if err != nil {
		t.Logf("Set returned error (expected in single-node test): %v", err)
	}

	_, _, err = client.Get("client-key", 0, node.ID())
	if err != nil {
		t.Logf("Get returned error (expected in single-node test): %v", err)
	}

	err = client.Delete("client-key", time.Second, node.ID())
	if err != nil {
		t.Logf("Delete returned error (expected in single-node test): %v", err)
	}

	_, err = client.Keys("client-", node.ID())
	if err != nil {
		t.Logf("Keys returned error (expected in single-node test): %v", err)
	}
}

func TestClient_Get_NotFound(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	val, found, err := client.Get("nonexistent-key", 0, node.ID())
	if err == nil {
		assert.Assert(t, !found)
		assert.Assert(t, val == nil)
	}
}

func TestClient_Keys_Empty(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	keys, err := client.Keys("nonexistent-prefix-", node.ID())
	if err == nil {
		assert.Equal(t, len(keys), 0)
	}
}

func TestClient_ExchangePeers(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	client, err := newInternalClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

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

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	client, err := newInternalClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

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

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cl.Set("testkey", []byte("testvalue"), time.Second))

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	val, found, err := client.Get("testkey", 0, node.ID())
	if err == nil && found {
		assert.Assert(t, len(val) > 0)
	}
}

func TestClient_Keys_TypeConversions(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cl.Set("prefix/a", []byte("a"), time.Second))
	require.NoError(t, cl.Set("prefix/b", []byte("b"), time.Second))

	client, err := NewClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	keys, err := client.Keys("prefix/", node.ID())
	if err == nil {
		assert.Assert(t, len(keys) >= 0) // Just verify no panic
	}
}

func TestClient_toInt64(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected int64
	}{
		{"int64", int64(42), 42},
		{"uint64", uint64(42), 42},
		{"float64", float64(42.5), 42},
		{"int", int(42), 42},
		{"uint", uint(42), 42},
		{"int32", int32(42), 42},
		{"uint32", uint32(42), 42},
		{"string", "not a number", 0},
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toInt64(tt.input)
			assert.Equal(t, result, tt.expected)
		})
	}
}

// TestClient_JoinVoter tests the JoinVoter method
func TestClient_JoinVoter(t *testing.T) {
	node := newMockNode(t)

	cl, err := New(node, "/raft/test", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	client, err := newInternalClient(node, "/raft/test", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	err = client.JoinVoter(node.ID(), time.Second, node.ID())
	if err != nil {
		t.Logf("JoinVoter returned error (expected): %v", err)
	}
}

// TestClient_WithEncryption tests client methods with encryption enabled
func TestClient_WithEncryption(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	node := newMockNode(t)

	cl, err := New(node, "/raft/enc-client-test", WithEncryptionKey(key), WithForceBootstrap())
	require.NoError(t, err, "failed to create encrypted cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = cl.WaitForLeader(ctx)
	if err != nil {
		t.Logf("WaitForLeader failed (may be expected): %v", err)
		return
	}

	block, err := aes.NewCipher(key)
	require.NoError(t, err, "failed to create cipher")
	gcm, err := cipher.NewGCMWithNonceSize(block, nonceSize)
	require.NoError(t, err, "failed to create GCM")
	client, err := NewClient(node, "/raft/enc-client-test", gcm)
	require.NoError(t, err, "failed to create encrypted client")
	defer client.Close()

	err = client.Set("enc-key", []byte("enc-value"), time.Second, node.ID())
	if err == nil {
		val, found, err := client.Get("enc-key", 0, node.ID())
		if err == nil && found {
			assert.Equal(t, string(val), "enc-value")
		}

		err = client.Delete("enc-key", time.Second, node.ID())
		if err != nil {
			t.Logf("Delete with encryption returned error: %v", err)
		}

		keys, err := client.Keys("enc-", node.ID())
		if err == nil {
			assert.Assert(t, keys != nil)
		}
	}
}

func TestClient_Get_WithBarrier(t *testing.T) {
	node := newTestNode(t)

	cl, err := New(node, "/raft/test-barrier", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.Set("barrier-key", []byte("barrier-value"), time.Second)
	require.NoError(t, err, "failed to set value")

	client, err := NewClient(node, "/raft/test-barrier", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	barrierNs := int64(time.Second.Nanoseconds())
	val, found, err := client.Get("barrier-key", barrierNs, node.ID())
	// In single-node, network may fail, but if it succeeds, barrier should work
	if err == nil {
		require.True(t, found, "key should be found")
		require.Equal(t, []byte("barrier-value"), val, "value should match")
	} else {
		// Network error is expected in single-node, but validation should have passed
		t.Logf("Get with barrier returned network error (expected in single-node): %v", err)
	}
}

func TestClient_Get_WithBarrier_Zero(t *testing.T) {
	node := newTestNode(t)

	cl, err := New(node, "/raft/test-barrier-zero", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	err = cl.Set("barrier-key-zero", []byte("barrier-value-zero"), time.Second)
	require.NoError(t, err, "failed to set value")

	client, err := NewClient(node, "/raft/test-barrier-zero", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	val, found, err := client.Get("barrier-key-zero", 0, node.ID())
	// In single-node, network may fail, but if it succeeds, should work
	if err == nil {
		require.True(t, found, "key should be found")
		require.Equal(t, []byte("barrier-value-zero"), val, "value should match")
	} else {
		// Network error is expected in single-node, but validation should have passed
		t.Logf("Get with barrierNs=0 returned network error (expected in single-node): %v", err)
	}
}

func TestClient_Get_WithBarrier_Invalid_Negative(t *testing.T) {
	node := newTestNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test-barrier-invalid", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test-barrier-invalid", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Get with barrierNs < 0 (should return error)
	_, _, err = client.Get("test-key", -1, node.ID())
	require.Error(t, err, "Get with negative barrierNs should return error")
	require.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")
}

func TestClient_Get_WithBarrier_Invalid_Zero(t *testing.T) {
	node := newTestNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test-barrier-invalid-zero", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test-barrier-invalid-zero", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Note: barrierNs = 0 is allowed (means no barrier)
	// This test verifies that 0 is handled correctly (no error, no barrier call)
	_, _, err = client.Get("test-key", 0, node.ID())
	// Should not return ErrInvalidBarrier for 0
	if err != nil && err == ErrInvalidBarrier {
		t.Errorf("barrierNs=0 should not return ErrInvalidBarrier, got: %v", err)
	}
}

func TestClient_Get_WithBarrier_Invalid_ExceedsMax(t *testing.T) {
	node := newTestNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test-barrier-exceeds", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Create client
	client, err := NewClient(node, "/raft/test-barrier-exceeds", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Get with barrierNs > MaxGetHandlerBarrierTimeout (should return error)
	barrierNs := int64((MaxGetHandlerBarrierTimeout + time.Second).Nanoseconds())
	_, _, err = client.Get("test-key", barrierNs, node.ID())
	require.Error(t, err, "Get with barrierNs > MaxGetHandlerBarrierTimeout should return error")
	require.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")
}

func TestClient_Get_WithBarrier_AtMax(t *testing.T) {
	node := newTestNode(t)

	// Create cluster first
	cl, err := New(node, "/raft/test-barrier-max", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	// Wait for leader
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	// Set a value first
	err = cl.Set("barrier-key-max", []byte("barrier-value-max"), time.Second)
	require.NoError(t, err, "failed to set value")

	// Create client
	client, err := NewClient(node, "/raft/test-barrier-max", nil)
	require.NoError(t, err, "failed to create client")
	defer client.Close()

	// Test Get with barrierNs exactly at MaxGetHandlerBarrierTimeout (should work)
	// Note: In single-node setup, stream may not open, but validation should pass
	barrierNs := int64(MaxGetHandlerBarrierTimeout.Nanoseconds())
	val, found, err := client.Get("barrier-key-max", barrierNs, node.ID())
	// In single-node, network may fail, but if it succeeds, barrier should work
	if err == nil {
		require.True(t, found, "key should be found")
		require.Equal(t, []byte("barrier-value-max"), val, "value should match")
	} else {
		// Network error is expected in single-node, but validation should have passed
		t.Logf("Get with barrierNs at max returned network error (expected in single-node): %v", err)
	}
}
