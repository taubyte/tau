package proxy

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
)

// The peek loop is the interesting part: the proxy reads what the router asked for,
// replays it to the workload, then splices.

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
func routing(t *testing.T, answer func([]byte) *invoke.Direction, cfg Config) (Socket, *steering, *workers, *warnings) {
	t.Helper()

	res := servable()
	routed(res)

	st := &steering{answer: answer}
	w := &workers{said: "pong:", direct: st.direct}

	said := &warnings{}
	if cfg.CloudFqdn == "" {
		cfg.CloudFqdn = "tau.example.io"
	}
	s, err := build(cfg,
		withProjects(projects{p: compiled(t, res)}),
		withWorkers(w),
		withWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)
	return sock, st, w, said
}

// says opens a connection, writes body, half-closes, and reads the answer.
func says(t *testing.T, sock Socket, body string) string {
	t.Helper()

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
	require.NoError(t, err)
	defer c.Close()

	// Neither write nor half-close must succeed: a refused connection closes early, and
	// what is asserted is what came back.
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte(body))
	c.(*net.TCPConn).CloseWrite()

	got, _ := io.ReadAll(c)
	return string(got)
}

func TestARouterPeeksAndThePeekedBytesStillArrive(t *testing.T) {
	sock, st, w, _ := routing(t, func(peeked []byte) *invoke.Direction {
		if len(peeked) < 6 {
			return &invoke.Direction{More: 6}
		}
		return &invoke.Direction{To: &invoke.Endpoint{Kind: "service", Name: "db", Port: "pg"}}
	}, Config{})

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

func TestARouterIsToldTheSourceAndTheName(t *testing.T) {
	sock, st, _, _ := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{To: &invoke.Endpoint{Kind: "service", Name: "db", Port: "pg"}}
	}, Config{})

	says(t, sock, "hello")

	conn := st.told()
	require.NotNil(t, conn)
	require.True(t, strings.HasPrefix(conn.Source, "127.0.0.1:"), "the peer address, got %q", conn.Source)
	require.Equal(t, "postgres", conn.Protocol)
	require.Equal(t, "tcp", conn.Transport)
	require.True(t, strings.HasSuffix(conn.Host, ".tau.example.io"), "the name it answers on, got %q", conn.Host)
}

func TestARouterCanRefuseAConnection(t *testing.T) {
	sock, _, w, said := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{Refused: "not from there"}
	}, Config{})

	require.Empty(t, says(t, sock, "hello"))

	at, _ := w.asked()
	require.Empty(t, at.Name, "no workload was reached")
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

func TestARouterCannotAskForMoreThanTheProxyAllows(t *testing.T) {
	sock, _, w, said := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{More: 1 << 20}
	}, Config{Peek: 64})

	require.Empty(t, says(t, sock, "hello"))
	at, _ := w.asked()
	require.Empty(t, at.Name)
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

func TestARouterThatWantsNoMoreThanItHasIsRefused(t *testing.T) {
	sock, _, w, said := routing(t, func(peeked []byte) *invoke.Direction {
		return &invoke.Direction{More: uint32(len(peeked))}
	}, Config{})

	require.Empty(t, says(t, sock, "hello"))
	at, _ := w.asked()
	require.Empty(t, at.Name)
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}

func TestAConnectionThatSaysNothingIsNotWaitedOnForever(t *testing.T) {
	sock, _, w, _ := routing(t, func(peeked []byte) *invoke.Direction {
		return &invoke.Direction{More: uint32(len(peeked) + 16)}
	}, Config{Decide: 150 * time.Millisecond})

	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))))
	require.NoError(t, err)
	defer c.Close()

	c.SetDeadline(time.Now().Add(3 * time.Second))
	got, _ := io.ReadAll(c)
	require.Empty(t, got)

	at, _ := w.asked()
	require.Empty(t, at.Name)
}

func TestARouterIsAskedOnceMoreWhenTheClientIsDone(t *testing.T) {
	var asked int
	sock, st, w, _ := routing(t, func(peeked []byte) *invoke.Direction {
		asked++
		if !bytes.Contains(peeked, []byte("hi")) {
			return &invoke.Direction{More: 64}
		}
		return &invoke.Direction{To: &invoke.Endpoint{Kind: "service", Name: "db", Port: "pg"}}
	}, Config{})

	require.Equal(t, "pong:hi", says(t, sock, "hi"))

	at, _ := w.asked()
	require.Equal(t, "db", at.Name)
	require.GreaterOrEqual(t, len(st.peeks()), 2)
}

func TestARouterCanNameANamespace(t *testing.T) {
	sock, _, w, _ := routing(t, func([]byte) *invoke.Direction {
		return &invoke.Direction{To: &invoke.Endpoint{
			Kind: "service", Namespace: "/local", Name: "db", Port: "pg",
		}}
	}, Config{})

	says(t, sock, "hello")

	at, _ := w.asked()
	require.Equal(t, []string{"local"}, at.Scope)
}

func TestARouterRoutedProxyThatNamesNoRouterIsRefused(t *testing.T) {
	res := servable()
	routed(res)
	delete(res, "router")

	said := &warnings{}
	w := &workers{}
	s, err := build(Config{CloudFqdn: "tau.example.io"},
		withProjects(projects{p: compiled(t, res)}),
		withWorkers(w),
		withWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	sock, err := s.Serve(t.Context(), pgwire())
	require.NoError(t, err)

	require.Empty(t, says(t, sock, "hello"))
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond)
}
