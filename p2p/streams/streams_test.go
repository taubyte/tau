package streams

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	keypair "github.com/taubyte/tau/p2p/keypair"
	peer "github.com/taubyte/tau/p2p/peer"
)

func TestStreamManger_Context(t *testing.T) {
	ctx := context.Background()

	// We can't easily test New without a full peer.Node mock,
	// but we can test the StreamManger struct directly
	sm := &StreamManger{
		ctx:  ctx,
		name: "test",
		path: "/test/path",
	}

	assert.Equal(t, ctx, sm.Context())
}

func TestStreamManger_Fields(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sm := &StreamManger{
		ctx:        ctx,
		ctx_cancel: cancel,
		name:       "testService",
		path:       "/test/v1.0.0",
	}

	assert.Equal(t, ctx, sm.Context())
}

func TestConnection_Interface(t *testing.T) {
	// Test that Connection interface is defined correctly
	var _ Connection = nil // compile-time check
}

func TestStream_Type(t *testing.T) {
	// Test that Stream type is defined correctly
	var _ Stream = nil // compile-time check
}

func TestStreamHandler_Type(t *testing.T) {
	// Test that StreamHandler func type works
	var handler StreamHandler = func(s Stream) {
		// Handler implementation
	}
	assert.NotNil(t, handler)
}

func TestNew(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 12001)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	sm := New(p1, "test-service", "/test/v1.0.0")
	require.NotNil(t, sm)

	assert.NotNil(t, sm.Context())
}

func TestStreamManger_StartAndStop(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 12002)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	sm := New(p1, "start-stop-test", "/start-stop/v1.0.0")
	require.NotNil(t, sm)

	sm.Start(func(s Stream) {
		// Handler
	})

	sm.Stop()
}

func TestStreamManger_MultipleStartStop(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 12003)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	sm := New(p1, "multi-test", "/multi/v1.0.0")
	require.NotNil(t, sm)

	sm.Start(func(s Stream) {})
	sm.Stop()

	// Start again with different handler
	sm2 := New(p1, "multi-test-2", "/multi2/v1.0.0")
	require.NotNil(t, sm2)
	sm2.Start(func(s Stream) {})
	sm2.Stop()
}
