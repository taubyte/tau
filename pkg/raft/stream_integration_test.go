//go:build raft_integration

package raft

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/streams/command"
	"gotest.tools/v3/assert"
)

func TestStreamService_HandleGet_WithBarrier_Integration(t *testing.T) {
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

func TestStreamService_HandleGet_WithBarrier_Invalid_Integration(t *testing.T) {
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
