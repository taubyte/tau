package proxy_test

import (
	"bytes"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/services/proxy"
)

// route_test drives a router over a real socket, because the peek loop is the
// interesting part: the proxy reads exactly what the router asked for, replays
// it to the workload, and everything after it is an ordinary splice.

func routed(r map[string]any) {
	r["routing"] = "router"
	r["router"] = map[string]any{"name": "usher"}
	delete(r, "forward")
}

// steering is a router fake that records every peek it was shown.
type steering struct {
	mu   sync.Mutex
	saw  [][]byte
	conn *invoke.Connection

	answer func(peeked []byte) *invoke.Direction
}

func (s *steering) direct(conn *invoke.Connection, peeked []byte) (*invoke.Direction, error) {
	s.mu.Lock()
	s.conn = conn
	s.saw = append(s.saw, append([]byte(nil), peeked...))
	s.mu.Unlock()
	return s.answer(peeked), nil
}

func (s *steering) peeks() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.saw...)
}

func (s *steering) told() *invoke.Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

// routing is a proxy whose destination one function decides.
func routing(t *testing.T, answer func([]byte) *invoke.Direction, cfg proxy.Config) (proxy.Socket, *steering, *workers, *warnings) {
	t.Helper()

	res := servable()
	routed(res)

	st := &steering{answer: answer}
	w := &workers{said: "pong:", direct: st.direct}

	said := &warnings{}
	if cfg.CloudFqdn == "" {
		cfg.CloudFqdn = "tau.example.io"
	}
	s, err := proxy.New(cfg,
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(w),
		proxy.WithWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)
	return sock, st, w, said
}

// says opens a connection, writes body, half-closes, and reads the answer.
func says(t *testing.T, sock proxy.Socket, body string) string {
	t.Helper()

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
	require.NoError(t, err)
	defer c.Close()

	// NEITHER WRITE NOR HALF-CLOSE IS REQUIRED TO SUCCEED. A refused connection
	// is closed before the client has finished saying anything, which is
	// exactly the case half of these tests drive — and what is being asserted
	// is what came back, which is nothing.
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(body))
	c.(*net.TCPConn).CloseWrite()

	got, _ := io.ReadAll(c)
	return string(got)
}

// TestAROUTERREADSASMUCHASITASKSFORANDNOMORE, and what it read still reaches
// the workload — otherwise a protocol's greeting would be eaten by the
// classifier that read it.
func TestARouterPeeksAndThePeekedBytesStillArrive(t *testing.T) {
	sock, st, w, _ := routing(t, func(peeked []byte) *invoke.Direction {
		if len(peeked) < 6 {
			return &invoke.Direction{More: 6}
		}
		return &invoke.Direction{To: &invoke.Endpoint{Kind: "application", Name: "db", Port: "pg"}}
	}, proxy.Config{})

	require.Equal(t, "pong:pgwire-hello", says(t, sock, "pgwire-hello"),
		"the whole conversation reached the workload, peek included")

	at, port := w.asked()
	require.Equal(t, "db", at.Name)
	require.Equal(t, "pg", port, "the port the router named, not the one the config did")

	peeks := st.peeks()
	require.Len(t, peeks, 2, "asked once with nothing, once with what it wanted")
	require.Empty(t, peeks[0])
	require.Equal(t, "pgwire", string(peeks[1]))
}

// TestTHEROUTERISTOLDWHOISCALLINGANDWHATTHEYASKEDFOR.
func TestARouterIsToldTheSourceAndTheName(t *testing.T) {
	sock, st, _, _ := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{To: &invoke.Endpoint{Kind: "application", Name: "db", Port: "pg"}}
	}, proxy.Config{})

	says(t, sock, "hello")

	conn := st.told()
	require.NotNil(t, conn)
	require.True(t, strings.HasPrefix(conn.Source, "127.0.0.1:"), "the peer address, got %q", conn.Source)
	require.Equal(t, "postgres", conn.Protocol)
	require.Equal(t, "tcp", conn.Transport)
	require.True(t, strings.HasSuffix(conn.Host, ".tau.example.io"), "the name it answers on, got %q", conn.Host)
}

// TestAREFUSALCLOSESWITHNOTHINGWRITTEN, and the reason goes to the operator
// because a raw socket has nowhere to put one.
func TestARouterCanRefuseAConnection(t *testing.T) {
	sock, _, w, said := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{Refused: "not from there"}
	}, proxy.Config{})

	require.Empty(t, says(t, sock, "hello"))

	at, _ := w.asked()
	require.Empty(t, at.Name, "no workload was reached")
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

// TestAROUTERMAYNOTREADFOREVER. A router asking for more is a client holding
// this listener open by sending nothing, so the ceiling is the proxy's.
func TestARouterCannotAskForMoreThanTheProxyAllows(t *testing.T) {
	sock, _, w, said := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{More: 1 << 20}
	}, proxy.Config{Peek: 64})

	require.Empty(t, says(t, sock, "hello"))
	at, _ := w.asked()
	require.Empty(t, at.Name)
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

// TestAROUTERTHATNEVERGETSTHERE. A router asking for no more than it has
// already been shown would spin the peek loop on one prefix forever.
func TestARouterThatWantsNoMoreThanItHasIsRefused(t *testing.T) {
	sock, _, w, said := routing(t, func(peeked []byte) *invoke.Direction {
		return &invoke.Direction{More: uint32(len(peeked))}
	}, proxy.Config{})

	require.Empty(t, says(t, sock, "hello"))
	at, _ := w.asked()
	require.Empty(t, at.Name)
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

// TestACLIENTTHATSENDSNOTHING is not a connection the router can classify, and
// it must not hold a slot until something else times out.
func TestAConnectionThatSaysNothingIsNotWaitedOnForever(t *testing.T) {
	sock, _, w, _ := routing(t, func(peeked []byte) *invoke.Direction {
		return &invoke.Direction{More: uint32(len(peeked) + 16)}
	}, proxy.Config{Decide: 150 * time.Millisecond})

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
	require.NoError(t, err)
	defer c.Close()

	c.SetDeadline(time.Now().Add(3 * time.Second))
	got, _ := io.ReadAll(c)
	require.Empty(t, got)

	at, _ := w.asked()
	require.Empty(t, at.Name)
}

// TestTHELASTWORD: a protocol whose whole greeting is shorter than the router
// guessed still gets an answer, rather than being cut off at EOF.
func TestARouterIsAskedOnceMoreWhenTheClientIsDone(t *testing.T) {
	var asked int
	sock, st, w, _ := routing(t, func(peeked []byte) *invoke.Direction {
		asked++
		if !bytes.Contains(peeked, []byte("hi")) {
			return &invoke.Direction{More: 64}
		}
		return &invoke.Direction{To: &invoke.Endpoint{Kind: "application", Name: "db", Port: "pg"}}
	}, proxy.Config{})

	require.Equal(t, "pong:hi", says(t, sock, "hi"))

	at, _ := w.asked()
	require.Equal(t, "db", at.Name)
	require.GreaterOrEqual(t, len(st.peeks()), 2)
}

// TestAROUTERNAMESANAMESPACE, resolved from the proxy's own scope the way an
// omitted namespace is everywhere in this vocabulary.
func TestARouterCanNameANamespace(t *testing.T) {
	sock, _, w, _ := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{To: &invoke.Endpoint{
			Kind: "application", Namespace: "/local", Name: "db", Port: "pg",
		}}
	}, proxy.Config{})

	says(t, sock, "hello")

	at, _ := w.asked()
	require.Equal(t, []string{"local"}, at.Scope)
}

// TestAPROXYWITHNOROUTERNAMED is refused rather than forwarded somewhere.
func TestARouterRoutedProxyThatNamesNoRouterIsRefused(t *testing.T) {
	res := servable()
	routed(res)
	delete(res, "router")

	said := &warnings{}
	w := &workers{}
	s, err := proxy.New(proxy.Config{CloudFqdn: "tau.example.io"},
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(w),
		proxy.WithWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)

	require.Empty(t, says(t, sock, "hello"))
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}
