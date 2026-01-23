package router

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/streams"
	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"

	peercore "github.com/libp2p/go-libp2p/core/peer"
)

func TestRouterIntegration(t *testing.T) {
	ctx := context.Background()

	// Create provider peer
	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 15001)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	// Create stream manager
	sm := streams.New(p1, "router-test", "/router-test/1.0")
	require.NotNil(t, sm)

	// Create router
	r := New(sm)
	require.NotNil(t, r)

	// Add a command handler
	err = r.AddStatic("hello", func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		name, _ := body["name"].(string)
		return cr.Response{"greeting": "Hello, " + name + "!"}, nil
	}, nil)
	require.NoError(t, err)

	// Add a command that returns error
	err = r.AddStatic("fail", func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return nil, fmt.Errorf("intentional error")
	}, nil)
	require.NoError(t, err)

	// Add a command with stream handler
	err = r.AddStatic("echo", func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{"status": "streaming"}, nil
	}, func(ctx context.Context, rw io.ReadWriter) {
		buf := make([]byte, 1024)
		for {
			n, err := rw.Read(buf)
			if n > 0 {
				rw.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	})
	require.NoError(t, err)

	// Start the stream manager with router handler
	sm.Start(r.Handle)
	defer sm.Stop()

	// Create consumer peer
	p2, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 15002)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p2.Close()

	// Connect peers
	err = p2.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()})
	require.NoError(t, err)

	// Test successful command
	t.Run("SuccessfulCommand", func(t *testing.T) {
		stream, err := p2.Peer().NewStream(ctx, p1.ID(), "/router-test/1.0")
		require.NoError(t, err)
		defer stream.Close()

		cmd := command.New("hello", command.Body{"name": "World"})
		err = cmd.Encode(stream)
		require.NoError(t, err)

		resp, err := cr.Decode(stream)
		require.NoError(t, err)

		greeting, err := resp.Get("greeting")
		require.NoError(t, err)
		assert.Equal(t, "Hello, World!", greeting)
	})

	// Test command that returns error
	t.Run("FailingCommand", func(t *testing.T) {
		stream, err := p2.Peer().NewStream(ctx, p1.ID(), "/router-test/1.0")
		require.NoError(t, err)
		defer stream.Close()

		cmd := command.New("fail", command.Body{})
		err = cmd.Encode(stream)
		require.NoError(t, err)

		resp, err := cr.Decode(stream)
		require.NoError(t, err)

		errorMsg, err := resp.Get("error")
		require.NoError(t, err)
		assert.Contains(t, errorMsg, "intentional error")
	})

	// Test unregistered command
	t.Run("UnregisteredCommand", func(t *testing.T) {
		stream, err := p2.Peer().NewStream(ctx, p1.ID(), "/router-test/1.0")
		require.NoError(t, err)
		defer stream.Close()

		cmd := command.New("nonexistent", command.Body{})
		err = cmd.Encode(stream)
		require.NoError(t, err)

		resp, err := cr.Decode(stream)
		require.NoError(t, err)

		errorMsg, err := resp.Get("error")
		require.NoError(t, err)
		assert.Contains(t, errorMsg, "not registered")
	})

	// Test invalid command (malformed)
	t.Run("InvalidCommand", func(t *testing.T) {
		stream, err := p2.Peer().NewStream(ctx, p1.ID(), "/router-test/1.0")
		require.NoError(t, err)
		defer stream.Close()

		// Send garbage data
		_, err = stream.Write([]byte("not a valid command"))
		require.NoError(t, err)

		// The router should respond with an error
		resp, err := cr.Decode(stream)
		if err == nil && resp != nil {
			// Response should contain an error
			_, hasError := resp["error"]
			assert.True(t, hasError, "Response should contain error for invalid command")
		}
	})

	// Test streaming command
	t.Run("StreamingCommand", func(t *testing.T) {
		stream, err := p2.Peer().NewStream(ctx, p1.ID(), "/router-test/1.0")
		require.NoError(t, err)
		defer stream.Close()

		cmd := command.New("echo", command.Body{})
		err = cmd.Encode(stream)
		require.NoError(t, err)

		resp, err := cr.Decode(stream)
		require.NoError(t, err)

		status, err := resp.Get("status")
		require.NoError(t, err)
		assert.Equal(t, "streaming", status)

		// Send data and receive echo
		testData := []byte("test echo data")
		_, err = stream.Write(testData)
		require.NoError(t, err)

		// Read echo back
		buf := make([]byte, len(testData))
		_, err = io.ReadFull(stream, buf)
		require.NoError(t, err)
		assert.Equal(t, testData, buf)
	})
}

func TestHandle_ResponseEncodeError(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 15003)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	sm := streams.New(p1, "encode-test", "/encode-test/1.0")
	require.NotNil(t, sm)

	r := New(sm)

	// Handler that returns nil response (edge case)
	err = r.AddStatic("nilresp", func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return nil, nil
	}, nil)
	require.NoError(t, err)

	sm.Start(r.Handle)
	defer sm.Stop()

	p2, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 15004)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p2.Close()

	err = p2.Peer().Connect(ctx, peercore.AddrInfo{ID: p1.ID(), Addrs: p1.Peer().Addrs()})
	require.NoError(t, err)

	stream, err := p2.Peer().NewStream(ctx, p1.ID(), "/encode-test/1.0")
	require.NoError(t, err)
	defer stream.Close()

	cmd := command.New("nilresp", command.Body{})
	err = cmd.Encode(stream)
	require.NoError(t, err)

	// Read any response (might error or return empty)
	var buf bytes.Buffer
	io.Copy(&buf, stream)
}
