//go:build raft_integration

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

func TestStreamService_HandleGet_WithBarrier_Integration(t *testing.T) {
	node := newTestNode(t)

	cl, err := New(node, "/raft/test-barrier", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	require.NoError(t, cl.Set("barrier-key", []byte("barrier-value"), time.Second), "failed to set value")

	c := getClusterInternal(cl)
	barrier := time.Second.Nanoseconds()
	resp, err := c.current().streamService.Get(context.Background(), connect.NewRequest(&raftv2.GetRequest{Key: "barrier-key", BarrierNs: &barrier}))
	require.NoError(t, err, "Get with barrier should succeed")
	require.True(t, resp.Msg.GetFound(), "expected found to be true")
	assert.Equal(t, "barrier-value", string(resp.Msg.GetValue()))
}

func TestStreamService_HandleGet_WithBarrier_Invalid_Integration(t *testing.T) {
	node := newTestNode(t)

	cl, err := New(node, "/raft/test-barrier-invalid", testOptions()...)
	require.NoError(t, err, "failed to create cluster")
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	require.NoError(t, cl.WaitForLeader(ctx), "failed to wait for leader")

	c := getClusterInternal(cl)

	negative := int64(-1)
	_, err = c.current().streamService.Get(context.Background(), connect.NewRequest(&raftv2.GetRequest{Key: "test-key", BarrierNs: &negative}))
	require.Error(t, err, "handleGet with negative barrier should fail")
	require.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")

	over := (MaxGetHandlerBarrierTimeout + time.Second).Nanoseconds()
	_, err = c.current().streamService.Get(context.Background(), connect.NewRequest(&raftv2.GetRequest{Key: "test-key", BarrierNs: &over}))
	require.Error(t, err, "handleGet with barrier > max should fail")
	require.ErrorIs(t, err, ErrInvalidBarrier, "should return ErrInvalidBarrier")
}
