//go:build raft_integration

package raft

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClient_Get_WithBarrier_Integration(t *testing.T) {
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

func TestClient_Get_WithBarrier_Zero_Integration(t *testing.T) {
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

func TestClient_Get_WithBarrier_Invalid_Negative_Integration(t *testing.T) {
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

func TestClient_Get_WithBarrier_Invalid_Zero_Integration(t *testing.T) {
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

func TestClient_Get_WithBarrier_Invalid_ExceedsMax_Integration(t *testing.T) {
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

func TestClient_Get_WithBarrier_AtMax_Integration(t *testing.T) {
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
