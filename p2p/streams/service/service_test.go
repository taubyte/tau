package service

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	keypair "github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/streams"

	peer "github.com/taubyte/tau/p2p/peer"

	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func TestNewService(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New( // provider
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11001)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("Peer creation returned error `%s`", err.Error())
		return
	}
	defer p1.Close()

	svr, err := New(p1, "hello", "/hello/1.0")
	require.NoError(t, err)
	require.NotNil(t, svr)
	defer svr.Stop()

	err = svr.Define("hi", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"message": "HI"}, nil
	})
	require.NoError(t, err)
}

func TestService_Router(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11002)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	svr, err := New(p1, "test", "/test/1.0")
	require.NoError(t, err)
	defer svr.Stop()

	router := svr.Router()
	assert.NotNil(t, router)
}

func TestService_DefineStream(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11003)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	svr, err := New(p1, "stream-test", "/stream-test/1.0")
	require.NoError(t, err)
	defer svr.Stop()

	cmdHandler := func(ctx context.Context, conn streams.Connection, body command.Body) (cr.Response, error) {
		return cr.Response{"status": "ok"}, nil
	}

	streamHandler := func(ctx context.Context, rw io.ReadWriter) {
		// Stream handler
	}

	err = svr.DefineStream("streamCmd", cmdHandler, streamHandler)
	require.NoError(t, err)
}

func TestService_Stop(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11004)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	svr, err := New(p1, "stop-test", "/stop-test/1.0")
	require.NoError(t, err)

	// Define a command
	err = svr.Define("test", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{}, nil
	})
	require.NoError(t, err)

	// Stop the service
	svr.Stop()
}

func TestService_Define_NilHandler(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11005)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	svr, err := New(p1, "nil-test", "/nil-test/1.0")
	require.NoError(t, err)
	defer svr.Stop()

	// Defining with nil handler should error
	err = svr.Define("nilCmd", nil)
	assert.Error(t, err)
}

func TestService_Define_DuplicateCommand(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New(
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11006)},
		nil,
		true,
		false,
	)
	require.NoError(t, err)
	defer p1.Close()

	svr, err := New(p1, "dup-test", "/dup-test/1.0")
	require.NoError(t, err)
	defer svr.Stop()

	handler := func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{}, nil
	}

	err = svr.Define("dupCmd", handler)
	require.NoError(t, err)

	// Defining same command again should error
	err = svr.Define("dupCmd", handler)
	assert.Error(t, err)
}

func TestCommandServiceInterface(t *testing.T) {
	// Verify commandService implements CommandService interface
	var _ CommandService = (*commandService)(nil)
}
