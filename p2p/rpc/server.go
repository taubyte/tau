package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// clientPreface is the greeting a cleartext HTTP/2 client opens with, and the
// threshold this package's deadline runs to. net/http peeks all 24 bytes with no
// deadline of its own before handing the conn to http2, whose timeouts start after.
const clientPreface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

// DefaultPrefaceTimeout is how long a freshly accepted stream has to deliver
// that preface. Without it a peer can open streams and never finish speaking,
// and each one parks a goroutine until the peer goes away.
const DefaultPrefaceTimeout = 10 * time.Second

// ErrProtocolTaken is returned by Listen when the host already answers on the
// protocol. libp2p would let the second listener silently replace the first.
var ErrProtocolTaken = errors.New("protocol already has a stream handler")

// ErrServerFieldOwned is returned by Listen when WithHTTPServer sets a field
// this package needs to keep its promises.
var ErrServerFieldOwned = errors.New("field is owned by this package")

// ServerOption adjusts a Server before it starts accepting.
type ServerOption func(*Server)

// WithPrefaceTimeout replaces DefaultPrefaceTimeout. Zero lets an unfinished
// stream wait forever, which is only reasonable when something else already
// bounds who can open one.
func WithPrefaceTimeout(d time.Duration) ServerOption {
	return func(s *Server) { s.prefaceTimeout = d }
}

// WithHTTPServer tunes the http.Server before it serves: MaxHeaderBytes,
// IdleTimeout, ErrorLog, ConnState, and through HTTP2Config the concurrent stream
// limit and flow-control windows.
//
// Handler, Protocols, BaseContext and ConnContext are owned by this package (they
// keep the pipe HTTP/2-only, tie serving to Listen's context, and carry RemotePeer);
// setting one is ErrServerFieldOwned.
//
// ReadTimeout and WriteTimeout are per stream under HTTP/2, so a unary-sized number
// cuts off streaming calls; IdleTimeout is safer.
func WithHTTPServer(f func(*http.Server)) ServerOption {
	return func(s *Server) { s.tune = f }
}

// Server answers ConnectRPC on one libp2p protocol.
type Server struct {
	listener net.Listener
	http     *http.Server

	prefaceTimeout time.Duration
	tune           func(*http.Server)

	done     chan struct{}
	errMu    sync.Mutex
	serveErr error
}

// Listen registers id on h and serves handler over every stream opened on it.
// The listener is bound before Listen returns, so a protocol that is already
// taken is an error here rather than a service that never receives anything.
// Serving stops when ctx ends, or on Close or Shutdown.
func Listen(ctx context.Context, h host.Host, id protocol.ID, handler http.Handler, opts ...ServerOption) (*Server, error) {
	if h == nil {
		return nil, errors.New("no host to listen on")
	}
	if handler == nil {
		return nil, errors.New("no handler to serve")
	}
	if id == "" {
		return nil, errors.New("no protocol to listen on")
	}
	if slices.Contains(h.Mux().Protocols(), id) {
		return nil, fmt.Errorf("listening on %s: %w", id, ErrProtocolTaken)
	}

	s := &Server{prefaceTimeout: DefaultPrefaceTimeout, done: make(chan struct{})}
	for _, opt := range opts {
		opt(s)
	}

	hs := new(http.Server)
	if s.tune != nil {
		s.tune(hs)
		if err := refuseOwnedFields(hs); err != nil {
			return nil, fmt.Errorf("listening on %s: %w", id, err)
		}
	}
	hs.Handler = handler
	hs.Protocols = h2()
	hs.BaseContext = func(net.Listener) context.Context { return ctx }
	hs.ConnContext = carryRemotePeer

	l, err := gostream.Listen(h, id)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", id, err)
	}
	s.listener = &serverListener{Listener: l, prefaceTimeout: s.prefaceTimeout}
	s.http = hs

	go s.serve()
	go s.watch(ctx)

	return s, nil
}

// refuseOwnedFields names the first field WithHTTPServer set that Listen is
// about to overwrite, so a caller hears about it here instead of wondering
// later why their handler never runs.
func refuseOwnedFields(hs *http.Server) error {
	switch {
	case hs.Handler != nil:
		return fmt.Errorf("http.Server.Handler: %w", ErrServerFieldOwned)
	case hs.Protocols != nil:
		return fmt.Errorf("http.Server.Protocols: %w", ErrServerFieldOwned)
	case hs.BaseContext != nil:
		return fmt.Errorf("http.Server.BaseContext: %w", ErrServerFieldOwned)
	case hs.ConnContext != nil:
		return fmt.Errorf("http.Server.ConnContext: %w", ErrServerFieldOwned)
	}
	return nil
}

// serve runs until the server is asked to stop, or until the listener dies
// for some other reason worth remembering.
func (s *Server) serve() {
	defer close(s.done)
	if err := s.http.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		s.errMu.Lock()
		s.serveErr = err
		s.errMu.Unlock()
	}
}

// watch ties the server's life to the context it was started with.
func (s *Server) watch(ctx context.Context) {
	select {
	case <-ctx.Done():
		s.Close()
	case <-s.done:
	}
}

// Addr is the host's peer id: that is the only address a libp2p listener has.
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Close drops the listener and every stream on it at once. It is safe to call more
// than once, and on a server already draining.
//
// The listener is closed here too: net/http closes only listeners Serve has started
// tracking, and Serve's goroutine may not have started yet.
func (s *Server) Close() error { return errors.Join(s.http.Close(), s.listener.Close()) }

// Shutdown stops accepting and waits for the calls already in flight, or for
// ctx to end. Like Close, it has released the protocol when it returns.
func (s *Server) Shutdown(ctx context.Context) error {
	return errors.Join(s.http.Shutdown(ctx), s.listener.Close())
}

// Done closes once the server has stopped serving, however it stopped.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err reports why the server stopped, if it stopped for a reason other than
// being asked to.
func (s *Server) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.serveErr
}

// serverListener hands net/http a conn that remembers which peer opened it
// and that will not wait forever for that peer to finish its preface.
type serverListener struct {
	net.Listener
	prefaceTimeout time.Duration
	closed         sync.Once
}

// Close releases the protocol once, for whichever asks first: the Server, or
// net/http as Serve returns. A second close would remove whatever handler
// holds the protocol by then.
func (l *serverListener) Close() error {
	var err error
	l.closed.Do(func() { err = l.Listener.Close() })
	return err
}

func (l *serverListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	sc := &serverConn{Conn: c}
	if s, ok := c.(interface{ Conn() network.Conn }); ok {
		sc.remote = s.Conn().RemotePeer()
	}

	if l.prefaceTimeout > 0 {
		// A conn that will not take a deadline is still a usable conn; it
		// just cannot be held to this.
		if err := c.SetReadDeadline(time.Now().Add(l.prefaceTimeout)); err == nil {
			sc.deadlined = true
		}
	}

	return sc, nil
}

type serverConn struct {
	net.Conn
	remote    peer.ID
	deadlined bool

	toward atomic.Int64 // bytes read toward the preface
	lifted atomic.Bool
}

// RemotePeerID is the libp2p peer on the far end, read off the stream when it
// was accepted rather than off anything the peer sent.
func (c *serverConn) RemotePeerID() peer.ID { return c.remote }

// Read lifts the preface deadline once the peer has delivered the whole client
// preface, not one byte sooner: net/http is still inside its own Peek until then,
// and http2's timeouts do not exist yet. net/http caps its reads at the preface
// length, so the count is exact.
func (c *serverConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 && c.deadlined && !c.lifted.Load() {
		if c.toward.Add(int64(n)) >= int64(len(clientPreface)) && c.lifted.CompareAndSwap(false, true) {
			c.Conn.SetReadDeadline(time.Time{})
		}
	}
	return n, err
}
