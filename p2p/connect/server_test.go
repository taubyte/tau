package connect_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/connect"
	echov1 "github.com/taubyte/tau/p2p/connect/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/connect/testdata/gen/echo/v1/echov1connect"
)

func TestListenRejectsNonsense(t *testing.T) {
	h := newHost(t)
	mux := http.NewServeMux()

	_, err := connect.Listen(t.Context(), nil, testProtocol, mux)
	assert.Error(t, err)

	_, err = connect.Listen(t.Context(), h, testProtocol, nil)
	assert.Error(t, err)

	_, err = connect.Listen(t.Context(), h, "", mux)
	assert.Error(t, err)
}

// TestListenWillNotStealAProtocol: libp2p would let a second listener replace
// the first without a word, and the first service would simply stop hearing
// anything.
func TestListenWillNotStealAProtocol(t *testing.T) {
	h := newHost(t)
	first := serve(t, h, newEchoService())
	require.NotNil(t, first)

	_, err := connect.Listen(t.Context(), h, testProtocol, http.NewServeMux())
	assert.ErrorIs(t, err, connect.ErrProtocolTaken)
}

func TestAddrIsThePeerID(t *testing.T) {
	h := newHost(t)
	srv := serve(t, h, newEchoService())
	assert.Equal(t, h.ID().String(), srv.Addr().String())
}

func TestCloseStopsServing(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	require.NoError(t, h.server.Close())
	select {
	case <-h.server.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	assert.NoError(t, h.server.Err())

	// Closing again is not an error, and neither is asking for a graceful
	// shutdown of something already gone.
	assert.NoError(t, h.server.Close())
	assert.NoError(t, h.server.Shutdown(t.Context()))

	_, err = h.client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{}))
	assert.Error(t, err)
}

func TestShutdownDrains(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, h.server.Shutdown(ctx))

	select {
	case <-h.server.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
	assert.NoError(t, h.server.Err())
}

func TestServerStopsWithItsContext(t *testing.T) {
	h := newHost(t)
	ctx, cancel := context.WithCancel(t.Context())

	mux := http.NewServeMux()
	mux.Handle(echov1connect.NewEchoServiceHandler(newEchoService()))
	srv, err := connect.Listen(ctx, h, testProtocol, mux)
	require.NoError(t, err)

	cancel()
	select {
	case <-srv.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not stop the server")
	}
	assert.NoError(t, srv.Err())
}

// TestSilentStreamIsDropped: a peer that opens a stream and says nothing must
// not hold a server goroutine for as long as it likes.
func TestSilentStreamIsDropped(t *testing.T) {
	h1, h2 := twoHosts(t)

	mux := http.NewServeMux()
	mux.Handle(echov1connect.NewEchoServiceHandler(newEchoService()))
	srv, err := connect.Listen(t.Context(), h1, testProtocol, mux,
		connect.WithPrefaceTimeout(250*time.Millisecond))
	require.NoError(t, err)
	defer srv.Close()

	conn, err := gostream.Dial(t.Context(), h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Read(make([]byte, 1))
	assert.Error(t, err, "the server kept a silent stream open")
}

// TestHTTP1IsRefused: connect over HTTP/1.1 would answer unary calls and then
// hand back "streams" that only arrive once the handler returns. Better to
// have no connection than a quietly degraded one.
func TestHTTP1IsRefused(t *testing.T) {
	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService())

	http1 := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return gostream.Dial(ctx, h2, h1.ID(), testProtocol)
		},
	}}
	client := echov1connect.NewEchoServiceClient(http1, "http://"+h1.ID().String())

	_, err := client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{}))
	assert.Error(t, err)
}

// TestOneStreamCarriesEveryCall is the whole reason for HTTP/2 here: the cbor
// stack opens a libp2p stream per call.
func TestOneStreamCarriesEveryCall(t *testing.T) {
	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService())

	conn, err := connect.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer conn.Close()

	// Count the streams libp2p actually opened, not the requests made.
	before := openStreams(t, h2, h1.ID())
	client := echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL())
	for range 100 {
		_, err := client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{Payload: []byte("x")}))
		require.NoError(t, err)
	}
	after := openStreams(t, h2, h1.ID())

	assert.Equal(t, 1, after-before, "100 calls should ride one libp2p stream")
}

// openStreams counts the streams from h to pid that carry testProtocol.
func openStreams(t testing.TB, h host.Host, pid peer.ID) int {
	t.Helper()
	n := 0
	for _, c := range h.Network().ConnsToPeer(pid) {
		for _, s := range c.GetStreams() {
			if s.Protocol() == testProtocol {
				n++
			}
		}
	}
	return n
}

func TestClientRejectsNonsense(t *testing.T) {
	h := newHost(t)

	_, err := connect.NewClient(nil, h.ID(), testProtocol)
	assert.Error(t, err)

	_, err = connect.NewClient(h, peer.ID(""), testProtocol)
	assert.Error(t, err)

	_, err = connect.NewClient(h, h.ID(), "")
	assert.Error(t, err)
}

// TestClientCloseDropsTheStream: after Close the next call opens a new one
// rather than failing.
func TestClientCloseDropsTheStream(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	h.conn.Close()

	_, err = h.client.Unary(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{}))
	assert.NoError(t, err)
}

func TestClientURLNamesThePeer(t *testing.T) {
	h1, h2 := twoHosts(t)
	c, err := connect.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer c.Close()

	assert.Equal(t, "http://"+h1.ID().String(), c.URL())
	assert.NotNil(t, c.HTTP())
}

// TestUnreachablePeerFailsTheCall: nothing is dialled until the first call,
// so an unreachable peer surfaces there.
func TestUnreachablePeerFailsTheCall(t *testing.T) {
	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService())

	stranger := newHost(t)
	c, err := connect.NewClient(h2, stranger.ID(), testProtocol)
	require.NoError(t, err)
	defer c.Close()

	client := echov1connect.NewEchoServiceClient(c.HTTP(), c.URL())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = client.Unary(ctx, connectrpc.NewRequest(&echov1.EchoRequest{}))
	assert.Error(t, err)
	assert.NotErrorIs(t, err, io.EOF)
}

// TestCloseCutsShortADrain: Shutdown waits for calls in flight, and Close is
// how an operator stops waiting. They share no latch.
func TestCloseCutsShortADrain(t *testing.T) {
	h := newHarness(t)

	go h.client.Park(t.Context(), connectrpc.NewRequest(&echov1.EchoRequest{})) //nolint:errcheck // the call is meant to die with the server
	select {
	case <-h.svc.parked:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never ran")
	}

	drained := make(chan error, 1)
	go func() { drained <- h.server.Shutdown(context.Background()) }()

	select {
	case <-drained:
		t.Fatal("Shutdown returned while a call was still in flight")
	case <-time.After(250 * time.Millisecond):
	}

	require.NoError(t, h.server.Close())
	select {
	case err := <-drained:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not release the drain")
	}
}
