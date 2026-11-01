package rpc_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
)

func TestUnary(t *testing.T) {
	h := newHarness(t)

	for i := range 5 {
		payload := fmt.Appendf(nil, "round %d", i)
		res, err := h.client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: payload}))
		require.NoError(t, err)
		assert.Equal(t, payload, res.Msg.Payload)
	}
}

func TestServerStreamIsIncremental(t *testing.T) {
	h := newHarness(t)

	const gateHold = time.Second

	stream, err := h.client.Countdown(t.Context(), connect.NewRequest(&echov1.EchoRequest{
		Payload: []byte("tick"),
		Count:   3,
	}))
	require.NoError(t, err)
	defer stream.Close()

	start := time.Now()
	opened := make(chan struct{})
	go func() {
		// Hold the handler well past the point where an incremental stream
		// would have delivered its first message.
		<-opened
		time.Sleep(gateHold)
		close(h.svc.gate)
	}()

	first := make(chan time.Duration, 1)
	go func() {
		close(opened)
		if stream.Receive() {
			first <- time.Since(start)
		}
	}()

	var firstAt time.Duration
	select {
	case firstAt = <-first:
	case <-time.After(10 * time.Second):
		t.Fatal("first message never arrived: the stream is not incremental")
	}

	require.Equal(t, int32(0), stream.Msg().Index)
	assert.Less(t, firstAt, gateHold/2,
		"first message should arrive long before the handler is released, got %s", firstAt)

	// The rest follow once the handler is let go.
	for i := int32(1); i < 3; i++ {
		require.True(t, stream.Receive(), "message %d: %v", i, stream.Err())
		require.Equal(t, i, stream.Msg().Index)
	}
	require.False(t, stream.Receive())
	require.NoError(t, stream.Err())
	assert.GreaterOrEqual(t, time.Since(start), gateHold)
}

func TestClientStreamLargeBody(t *testing.T) {
	h := newHarness(t)

	const (
		chunk  = 64 << 10
		chunks = 128 // 8 MiB
	)

	stream := h.client.Absorb(t.Context())
	sum := sha256.New()
	buf := make([]byte, chunk)
	for range chunks {
		_, err := rand.Read(buf)
		require.NoError(t, err)
		sum.Write(buf)
		require.NoError(t, stream.Send(&echov1.EchoRequest{Payload: buf}))
	}

	res, err := stream.CloseAndReceive()
	require.NoError(t, err)
	assert.Equal(t, hex.EncodeToString(sum.Sum(nil)), res.Msg.Note)
	assert.Equal(t, int32(chunk*chunks), res.Msg.Index)
}

func TestBidiInterleaved(t *testing.T) {
	h := newHarness(t)

	stream := h.client.Volley(t.Context())
	defer stream.CloseResponse()

	for i := range 20 {
		payload := fmt.Appendf(nil, "volley %d", i)
		require.NoError(t, stream.Send(&echov1.EchoRequest{Payload: payload}))

		msg, err := stream.Receive()
		require.NoError(t, err, "receive %d", i)
		assert.Equal(t, payload, msg.Payload)
		assert.Equal(t, int32(i), msg.Index)
	}

	require.NoError(t, stream.CloseRequest())
	_, err := stream.Receive()
	assert.ErrorIs(t, err, io.EOF)
}

func TestClientCancelReachesHandler(t *testing.T) {
	h := newHarness(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := h.client.Park(ctx, connect.NewRequest(&echov1.EchoRequest{}))
		done <- err
	}()

	select {
	case <-h.svc.parked:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never ran")
	}

	cancel()

	select {
	case err := <-h.svc.parkErr:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not observe the client's cancellation")
	}

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Equal(t, connect.CodeCanceled, connect.CodeOf(err))
	case <-time.After(5 * time.Second):
		t.Fatal("client call did not return")
	}
}

func TestHandlerErrorKeepsItsCode(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Fail(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))

	var cerr *connect.Error
	require.True(t, errors.As(err, &cerr))
	assert.Contains(t, cerr.Message(), "the well is dry")
	assert.Equal(t, "deliberate", cerr.Meta().Get("Echo-Reason"))
}
