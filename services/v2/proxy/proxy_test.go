package proxy_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/project"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/v2/proxy"
)

// EVERY TEST HERE CARRIES REAL BYTES OVER REAL SOCKETS. What this service does
// is move bytes it cannot read, so a test that asserted on a decision without
// moving any would prove the half that is easy.

const projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"

// compiled is a project with one proxy forwarding to one application.
func compiled(t *testing.T, proxy map[string]any) *project.Project {
	t.Helper()

	doc := map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			"application": map[string]any{
				"QmApp": map[string]any{
					"name":   "db",
					"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/postgres:16"},
					"ports":  map[string]any{"pg": map[string]any{"number": 5432}},
				},
			},
			"proxy": map[string]any{"QmProxy": proxy},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/application/db/links": []any{
				"branches/main/projects/" + projectID + "/application/QmApp"},
			"projects/" + projectID + "/proxy/pgwire/links": []any{
				"branches/main/projects/" + projectID + "/proxy/QmProxy"},
		},
	}

	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	p, err := project.Load(raw)
	require.NoError(t, err)
	return p
}

func servable() map[string]any {
	return map[string]any{
		"name":     "pgwire",
		"protocol": "tcp",
		"service":  "postgres",
		"forward": map[string]any{
			"kind": "application", "name": "db", "port": "pg",
		},
	}
}

type projects struct{ p *project.Project }

func (s projects) At(context.Context, string, string) (*project.Project, register.Pointer, error) {
	return s.p, register.Pointer{}, nil
}

// workers records what it was asked for and answers on a real socket.
type workers struct {
	at   project.Address
	port string
	err  error
	said string
}

func (w *workers) Conn(_ context.Context, at project.Address, port string) (net.Conn, error) {
	w.at, w.port = at, port
	if w.err != nil {
		return nil, w.err
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		defer l.Close()
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		body, _ := io.ReadAll(c)
		c.Write([]byte(w.said + string(body)))
	}()
	return net.Dial("tcp", l.Addr().String())
}

// warnings collects what the service told the operator. It is a type rather
// than a slice because a connection is carried on its own goroutine: a test
// holding the slice HEADER would be holding a copy taken before anything was
// appended to it, which is how the first version of this passed nothing.
type warnings struct {
	mu   sync.Mutex
	said []string
}

func (w *warnings) add(format string, _ ...any) {
	w.mu.Lock()
	w.said = append(w.said, format)
	w.mu.Unlock()
}

func (w *warnings) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.said)
}

func serve(t *testing.T, p *project.Project, w *workers) (net.Listener, *warnings) {
	t.Helper()

	said := &warnings{}
	s, err := proxy.New(proxy.Config{},
		proxy.WithProjects(projects{p: p}),
		proxy.WithWorkers(w),
		proxy.WithWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	require.NoError(t, s.Accept(t.Context(), l, proxy.Bind{
		Project: "shop", Environment: "prod", Name: "pgwire",
	}))
	return l, said
}

// TestBytesCrossTheProxyToTheWorkload.
func TestAConnectionIsCarriedToTheWorkload(t *testing.T) {
	w := &workers{said: "pong:"}
	l, _ := serve(t, compiled(t, servable()), w)

	c, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Write([]byte("ping"))
	require.NoError(t, err)
	require.NoError(t, c.(*net.TCPConn).CloseWrite())

	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(c)
	require.NoError(t, err)
	require.Equal(t, "pong:ping", string(got), "the bytes went both ways")

	require.Equal(t, "application", w.at.Kind)
	require.Equal(t, "db", w.at.Name)
	require.Equal(t, "pg", w.port, "the workload's own port, by the name the author declared")
}

// TestTHEFORWARDSNamespaceIsTheProxysOwnScope, not the operator's bind — the
// rule an omitted namespace follows everywhere in this vocabulary.
func TestAnOmittedForwardNamespaceResolvesFromTheProxy(t *testing.T) {
	w := &workers{}
	l, _ := serve(t, compiled(t, servable()), w)

	c, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	c.(*net.TCPConn).CloseWrite()
	io.ReadAll(c)
	c.Close()

	require.Empty(t, w.at.Scope, "the proxy is at the root, so its forward resolves there")
}

// ── what it refuses, and why each is a refusal rather than a downgrade ──

func refusedBy(t *testing.T, change func(map[string]any)) {
	t.Helper()

	res := servable()
	change(res)

	w := &workers{}
	l, said := serve(t, compiled(t, res), w)

	c, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, _ := io.ReadAll(c)
	require.Empty(t, got, "a refused connection is closed with nothing written")
	c.Close()

	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond,
		"the operator is told why, because the client cannot be")
	require.Empty(t, w.at.Name, "and no workload was reached")
}

// A udp application chose to do without ordering; a stream imposes it, so
// packet loss becomes a latency spike and head-of-line blocking arrives free.
func TestUdpIsRefusedRatherThanCarriedOverTcp(t *testing.T) {
	refusedBy(t, func(r map[string]any) { r["protocol"] = "udp" })
}

// A proxy that asked for TLS and got a plaintext pipe would be a security
// control silently not applied.
func TestTerminatingTlsIsRefused(t *testing.T) {
	refusedBy(t, func(r map[string]any) {
		r["tls"] = map[string]any{"mode": "terminate"}
	})
}

func TestARouterRoutedProxyIsRefused(t *testing.T) {
	refusedBy(t, func(r map[string]any) {
		r["routing"] = "router"
		r["router"] = map[string]any{"name": "picker"}
	})
}

func TestTransformersAreRefusedRatherThanSkipped(t *testing.T) {
	refusedBy(t, func(r map[string]any) {
		r["transformers"] = []any{map[string]any{"name": "pg_audit"}}
	})
}

// TestAWORKLOADThatCannotBeReachedClosesTheConnection, because there is nowhere
// to put a reason on a raw socket.
func TestAnUnreachableWorkloadClosesTheConnection(t *testing.T) {
	w := &workers{err: context.DeadlineExceeded}
	l, said := serve(t, compiled(t, servable()), w)

	c, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, _ := io.ReadAll(c)
	c.Close()

	require.Empty(t, got)
	require.Eventually(t, func() bool { return said.len() > 0 }, 2*time.Second, 10*time.Millisecond,
		"and the operator is told")
}

// TestCLOSEStopsAcceptingAndLeavesOpenConnectionsAlone: cutting a live psql
// session to reload would be worse than refusing a new one.
func TestCloseStopsAcceptingNewConnections(t *testing.T) {
	s, err := proxy.New(proxy.Config{},
		proxy.WithProjects(projects{p: compiled(t, servable())}),
		proxy.WithWorkers(&workers{}),
	)
	require.NoError(t, err)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, s.Accept(t.Context(), l, proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"}))

	addr := l.Addr().String()
	require.NoError(t, s.Close())

	_, err = net.Dial("tcp", addr)
	require.Error(t, err, "the socket is gone")
}

func TestAProxyWithNoWorkersIsRefusedAtConstruction(t *testing.T) {
	_, err := proxy.New(proxy.Config{}, proxy.WithProjects(projects{}))
	require.ErrorIs(t, err, proxy.ErrNoWorkers)
}

func TestAProxyWithNoRegisterIsRefusedAtConstruction(t *testing.T) {
	_, err := proxy.New(proxy.Config{}, proxy.WithWorkers(&workers{}))
	require.ErrorIs(t, err, proxy.ErrNoProjects)
}

// derivingService is a proxy service that derives its sockets inside prefix.
func derivingService(t *testing.T, prefix string, res map[string]any) *proxy.Service {
	t.Helper()

	s, err := proxy.New(proxy.Config{Prefix: prefix},
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(&workers{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}
