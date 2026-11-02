package rpc_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/p2p/rpc"
	echov1 "github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1"
	"github.com/taubyte/tau/p2p/rpc/testdata/gen/echo/v1/echov1connect"
)

func TestListenRejectsNonsense(t *testing.T) {
	h := newHost(t)
	mux := http.NewServeMux()

	_, err := rpc.Listen(t.Context(), nil, testProtocol, mux)
	assert.Error(t, err)

	_, err = rpc.Listen(t.Context(), h, testProtocol, nil)
	assert.Error(t, err)

	_, err = rpc.Listen(t.Context(), h, "", mux)
	assert.Error(t, err)
}

func TestListenWillNotStealAProtocol(t *testing.T) {
	h := newHost(t)
	first := serve(t, h, newEchoService())
	require.NotNil(t, first)

	_, err := rpc.Listen(t.Context(), h, testProtocol, http.NewServeMux())
	assert.ErrorIs(t, err, rpc.ErrProtocolTaken)
}

func TestAddrIsThePeerID(t *testing.T) {
	h := newHost(t)
	srv := serve(t, h, newEchoService())
	assert.Equal(t, h.ID().String(), srv.Addr().String())
}

func TestCloseStopsServing(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
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

	_, err = h.client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	assert.Error(t, err)
}

func TestStoppingReleasesTheProtocolAtOnce(t *testing.T) {
	stops := map[string]func(*rpc.Server) error{
		"close":    (*rpc.Server).Close,
		"shutdown": func(srv *rpc.Server) error { return srv.Shutdown(t.Context()) },
	}
	for name, stop := range stops {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			for range 100 {
				srv, err := rpc.Listen(t.Context(), h, testProtocol, http.NewServeMux())
				require.NoError(t, err)
				require.NoError(t, stop(srv))
				require.NotContains(t, h.Mux().Protocols(), testProtocol, "the server stopped with its protocol still registered")
				select {
				case <-srv.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("server did not stop")
				}
				require.NoError(t, srv.Err())
			}
		})
	}
}

func TestShutdownDrains(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
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
	srv, err := rpc.Listen(ctx, h, testProtocol, mux)
	require.NoError(t, err)

	cancel()
	select {
	case <-srv.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not stop the server")
	}
	assert.NoError(t, srv.Err())
}

// clientPreface is written out so these tests speak the wire's 24 bytes and notice
// the package drifting from them.
const clientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

func TestAnUnfinishedPrefaceIsDropped(t *testing.T) {
	const timeout = 500 * time.Millisecond

	type testCase struct {
		name  string
		speak func(net.Conn)
	}
	for _, tc := range []testCase{
		{"nothing at all", func(net.Conn) {}},
		{"one byte", func(c net.Conn) { c.Write([]byte(clientPreface[:1])) }},
		{"the http/2 magic and no more", func(c net.Conn) { c.Write([]byte(clientPreface[:14])) }},
		{"all but the last byte", func(c net.Conn) { c.Write([]byte(clientPreface[:23])) }},
		{"some, a pause, then some more", func(c net.Conn) {
			c.Write([]byte(clientPreface[:14]))
			time.Sleep(timeout / 2)
			c.Write([]byte(clientPreface[14:23]))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h1, h2 := twoHosts(t)
			serve(t, h1, newEchoService(), rpc.WithPrefaceTimeout(timeout))

			conn, err := gostream.Dial(t.Context(), h2, h1.ID(), testProtocol)
			require.NoError(t, err)
			defer conn.Close()

			start := time.Now()
			tc.speak(conn)

			read := make(chan error, 1)
			go func() {
				_, err := conn.Read(make([]byte, 1))
				read <- err
			}()

			select {
			case err := <-read:
				assert.ErrorIs(t, err, io.EOF, "the stream ended for some reason other than the server closing it")
				assert.Greater(t, time.Since(start), timeout*3/4,
					"the stream went away too early to have been the preface deadline")
			case <-time.After(10 * time.Second):
				t.Fatal("the server is still holding a stream that never finished its preface")
			}
		})
	}
}

func TestASlowPrefaceIsNotDropped(t *testing.T) {
	const timeout = 3 * time.Second

	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService(), rpc.WithPrefaceTimeout(timeout))

	slow := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c, err := gostream.Dial(ctx, h2, h1.ID(), testProtocol)
			if err != nil {
				return nil, err
			}
			return &dribbleConn{Conn: c, left: len(clientPreface), pause: timeout / 60}, nil
		},
		Protocols: h2Only(),
	}}

	start := time.Now()
	client := echov1connect.NewEchoServiceClient(slow, "http://"+h1.ID().String())
	res, err := client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: []byte("slow")}))
	require.NoError(t, err, "a peer that finished its preface inside the window was dropped anyway")
	assert.Equal(t, []byte("slow"), res.Msg.Payload)
	assert.Greater(t, time.Since(start), timeout/4,
		"the preface arrived too quickly for this to have exercised a slow one")
}

// dribbleConn hands the first left bytes to the wire one at a time, pausing
// in between, so the far end sees them arrive slowly rather than at once.
type dribbleConn struct {
	net.Conn
	left  int
	pause time.Duration
}

func (c *dribbleConn) Write(b []byte) (int, error) {
	n := 0
	for len(b) > 0 && c.left > 0 {
		w, err := c.Conn.Write(b[:1])
		n += w
		if err != nil {
			return n, err
		}
		b, c.left = b[1:], c.left-1
		time.Sleep(c.pause)
	}
	if len(b) == 0 {
		return n, nil
	}
	w, err := c.Conn.Write(b)
	return n + w, err
}

func TestThePrefaceDeadlineDoesNotOutliveThePreface(t *testing.T) {
	const timeout = 250 * time.Millisecond

	h1, h2 := twoHosts(t)
	opened := acceptCounter(t, h1, rpc.WithPrefaceTimeout(timeout))

	c, err := rpc.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer c.Close()
	client := echov1connect.NewEchoServiceClient(c.HTTP(), c.URL())

	_, err = client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	time.Sleep(4 * timeout)

	_, err = client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err, "an idle connection was dropped by the preface deadline")
	assert.Equal(t, 1, opened(), "the call after the wait had to open a second connection")
}

// acceptCounter serves an echo service and returns how many connections it has
// accepted, which tells a surviving connection from a silent redial.
func acceptCounter(t testing.TB, h host.Host, opts ...rpc.ServerOption) func() int {
	t.Helper()
	var mu sync.Mutex
	n := 0
	serve(t, h, newEchoService(), append(opts, rpc.WithHTTPServer(func(s *http.Server) {
		s.ConnState = func(_ net.Conn, st http.ConnState) {
			if st == http.StateNew {
				mu.Lock()
				n++
				mu.Unlock()
			}
		}
	}))...)
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func TestWithHTTPServerTunesTheServer(t *testing.T) {
	type testCase struct {
		name  string
		idle  time.Duration
		conns int
	}
	for _, tc := range []testCase{
		{"with an idle timeout, the second call redials", 250 * time.Millisecond, 2},
		{"without one, both calls ride the same connection", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h1, h2 := twoHosts(t)

			var mu sync.Mutex
			var states []http.ConnState
			serve(t, h1, newEchoService(), rpc.WithHTTPServer(func(s *http.Server) {
				s.IdleTimeout = tc.idle
				s.ConnState = func(_ net.Conn, st http.ConnState) {
					mu.Lock()
					states = append(states, st)
					mu.Unlock()
				}
			}))

			c, err := rpc.NewClient(h2, h1.ID(), testProtocol)
			require.NoError(t, err)
			defer c.Close()
			client := echov1connect.NewEchoServiceClient(c.HTTP(), c.URL())

			_, err = client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
			require.NoError(t, err)
			time.Sleep(750 * time.Millisecond)
			_, err = client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
			require.NoError(t, err)

			mu.Lock()
			defer mu.Unlock()
			opened := 0
			for _, st := range states {
				if st == http.StateNew {
					opened++
				}
			}
			assert.Equal(t, tc.conns, opened, "connections accepted: %v", states)
		})
	}
}

func TestAMessageCeilingIsTheServicesToSet(t *testing.T) {
	const ceiling = 1 << 10

	h1, h2 := twoHosts(t)
	mux := http.NewServeMux()
	mux.Handle(echov1connect.NewEchoServiceHandler(newEchoService(), connect.WithReadMaxBytes(ceiling)))
	srv, err := rpc.Listen(t.Context(), h1, testProtocol, mux)
	require.NoError(t, err)
	t.Cleanup(func() { srv.Close() })

	c, err := rpc.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer c.Close()
	client := echov1connect.NewEchoServiceClient(c.HTTP(), c.URL())

	under, err := client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: make([]byte, ceiling/2)}))
	require.NoError(t, err)
	assert.Len(t, under.Msg.Payload, ceiling/2)

	_, err = client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: make([]byte, 4*ceiling)}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
}

func TestWithHTTPServerCannotTakeWhatThePipeOwns(t *testing.T) {
	for name, take := range map[string]func(*http.Server){
		"Handler":   func(s *http.Server) { s.Handler = http.NewServeMux() },
		"Protocols": func(s *http.Server) { s.Protocols = h2Only() },
		"BaseContext": func(s *http.Server) {
			s.BaseContext = func(net.Listener) context.Context { return context.Background() }
		},
		"ConnContext": func(s *http.Server) {
			s.ConnContext = func(ctx context.Context, _ net.Conn) context.Context { return ctx }
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)

			_, err := rpc.Listen(t.Context(), h, testProtocol, http.NewServeMux(), rpc.WithHTTPServer(take))
			require.ErrorIs(t, err, rpc.ErrServerFieldOwned)
			assert.Contains(t, err.Error(), name)
			assert.NotContains(t, h.Mux().Protocols(), testProtocol,
				"the protocol was registered by a Listen that failed")
		})
	}
}

func TestHTTP1IsRefused(t *testing.T) {
	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService())

	http1 := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return gostream.Dial(ctx, h2, h1.ID(), testProtocol)
		},
	}}
	client := echov1connect.NewEchoServiceClient(http1, "http://"+h1.ID().String())

	_, err := client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	assert.Error(t, err)
}

func TestOneStreamCarriesEveryCall(t *testing.T) {
	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService())

	conn, err := rpc.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer conn.Close()

	// Count the streams libp2p actually opened, not the requests made.
	before := openStreams(t, h2, h1.ID())
	client := echov1connect.NewEchoServiceClient(conn.HTTP(), conn.URL())
	for range 100 {
		_, err := client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{Payload: []byte("x")}))
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

	_, err := rpc.NewClient(nil, h.ID(), testProtocol)
	assert.Error(t, err)

	_, err = rpc.NewClient(h, peer.ID(""), testProtocol)
	assert.Error(t, err)

	_, err = rpc.NewClient(h, h.ID(), "")
	assert.Error(t, err)
}

func TestClientCloseDropsTheStream(t *testing.T) {
	h := newHarness(t)

	_, err := h.client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	require.NoError(t, err)

	h.conn.Close()

	_, err = h.client.Unary(t.Context(), connect.NewRequest(&echov1.EchoRequest{}))
	assert.NoError(t, err)
}

func TestClientURLNamesThePeer(t *testing.T) {
	h1, h2 := twoHosts(t)
	c, err := rpc.NewClient(h2, h1.ID(), testProtocol)
	require.NoError(t, err)
	defer c.Close()

	assert.Equal(t, "http://"+h1.ID().String(), c.URL())
	assert.NotNil(t, c.HTTP())
}

func TestUnreachablePeerFailsTheCall(t *testing.T) {
	h1, h2 := twoHosts(t)
	serve(t, h1, newEchoService())

	stranger := newHost(t)
	c, err := rpc.NewClient(h2, stranger.ID(), testProtocol)
	require.NoError(t, err)
	defer c.Close()

	client := echov1connect.NewEchoServiceClient(c.HTTP(), c.URL())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = client.Unary(ctx, connect.NewRequest(&echov1.EchoRequest{}))
	assert.Error(t, err)
	assert.NotErrorIs(t, err, io.EOF)
}

func TestCloseCutsShortADrain(t *testing.T) {
	h := newHarness(t)

	go h.client.Park(t.Context(), connect.NewRequest(&echov1.EchoRequest{})) //nolint:errcheck // the call is meant to die with the server
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
