package proxy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/proxy"
)

// Every test carries real bytes over real sockets.

// Every id is a real CID: a compiled resource is told apart from an access entry by
// its id passing the identity validator, so a made-up one is silently no resource.
const (
	projectID = "QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR"
	appID     = "QmZCstKkwsL212pMaf4xk4V5AbgVfJC2NNSJdu8UZtqSpS"
	proxyID   = "QmPZv3VQe4s7A18auX3Tjnc3Bxd6cURg94qXCJ2Smbdvqf"
	zoneID    = "QmU1Vy6xqpYjRFFZqucyZULup6svHhsajomz71zWnSz9gk"
)

// compiled is a project with one proxy forwarding to one service.
func compiled(t *testing.T, proxy map[string]any) *specs.Project {
	t.Helper()

	doc := map[string]any{
		"object": map[string]any{
			"id":   projectID,
			"name": "shop",
			"service": map[string]any{
				appID: map[string]any{
					"name":   "db",
					"source": map[string]any{"kind": "image", "image": "ghcr.io/acme/postgres:16"},
					"ports":  map[string]any{"pg": map[string]any{"number": 5432}},
				},
			},
			"zone": map[string]any{
				zoneID: map[string]any{"name": "shop", "fqdn": "shop.example.com"},
			},
			"proxy": map[string]any{proxyID: proxy},
		},
		"indexes": map[string]any{
			"projects/" + projectID + "/service/db/links": []any{
				"branches/main/projects/" + projectID + "/service/" + appID},
			"projects/" + projectID + "/proxy/pgwire/links": []any{
				"branches/main/projects/" + projectID + "/proxy/" + proxyID},
			"projects/" + projectID + "/zone/shop/links": []any{
				"branches/main/projects/" + projectID + "/zone/" + zoneID},
		},
	}

	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	p, err := specs.Load(raw)
	require.NoError(t, err)
	return p
}

func servable() map[string]any {
	return map[string]any{
		"name":    "pgwire",
		"service": "postgres",
		"host":    map[string]any{"kind": "generated", "name": "db"},
		"forward": map[string]any{
			"kind": "service", "name": "db", "port": "pg",
		},
	}
}

type projects struct{ p *specs.Project }

func (s projects) At(context.Context, register.Deployment) (*specs.Project, register.Pointer, error) {
	return s.p, register.Pointer{}, nil
}

// workers records what it was asked for, under a lock since each connection is
// carried on its own goroutine, and answers on a real socket.
type workers struct {
	mu   sync.Mutex
	at   specs.Address
	port string
	err  error
	said string

	// direct and shape are the two proxy worlds. A nil one means this fake
	// runs none, which is what a worker with no such function answers.
	direct func(*invoke.Connection, []byte) (*invoke.Direction, error)
	shape  func(specs.Address, *invoke.Stream) (*invoke.Shaping, error)
}

func (w *workers) Direct(_ context.Context, _ specs.Address, conn *invoke.Connection, peeked []byte) (*invoke.Direction, error) {
	w.mu.Lock()
	f := w.direct
	w.mu.Unlock()

	if f == nil {
		return nil, errors.New("that worker runs no proxy router")
	}
	return f(conn, peeked)
}

func (w *workers) Shape(_ context.Context, at specs.Address, st *invoke.Stream) (*invoke.Shaping, error) {
	w.mu.Lock()
	f := w.shape
	w.mu.Unlock()

	if f == nil {
		return nil, errors.New("that worker runs no proxy transformer")
	}
	return f(at, st)
}

// asked is the last address and port this was asked to reach.
func (w *workers) asked() (specs.Address, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.at, w.port
}

func (w *workers) Conn(_ context.Context, at specs.Address, port string) (net.Conn, error) {
	w.mu.Lock()
	w.at, w.port = at, port
	err := w.err
	said := w.said
	w.mu.Unlock()

	if err != nil {
		return nil, err
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
		c.Write([]byte(said + string(body)))
	}()
	return net.Dial("tcp", l.Addr().String())
}

// warnings collects what the service told the operator, shared across connection
// goroutines.
type warnings struct {
	mu   sync.Mutex
	said []string
}

// add formats what it was given, so tests can assert on the reason.
func (w *warnings) add(format string, args ...any) {
	w.mu.Lock()
	w.said = append(w.said, fmt.Sprintf(format, args...))
	w.mu.Unlock()
}

// first is what the operator was told, for a test asserting on the reason
// rather than only on there having been one.
func (w *warnings) first() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.said) == 0 {
		return ""
	}
	return w.said[0]
}

func (w *warnings) len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.said)
}

func serve(t *testing.T, p *specs.Project, w *workers) (net.Listener, *warnings) {
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

	require.Equal(t, "service", w.at.Kind)
	require.Equal(t, "db", w.at.Name)
	require.Equal(t, "pg", w.port, "the workload's own port, by the name the author declared")
}

func TestAnOmittedForwardNamespaceResolvesFromTheProxy(t *testing.T) {
	w := &workers{}
	l, _ := serve(t, compiled(t, servable()), w)

	c, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	c.(*net.TCPConn).CloseWrite()
	io.ReadAll(c)
	c.Close()

	at, _ := w.asked()
	require.Empty(t, at.Scope, "the proxy is at the root, so its forward resolves there")
}

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
	at, _ := w.asked()
	require.Empty(t, at.Name, "and no workload was reached")
}

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

// service is a proxy that binds its own sockets, the way a node runs it.
func service(t *testing.T, cfg proxy.Config, res map[string]any, w *workers) (*proxy.Service, *warnings) {
	t.Helper()

	said := &warnings{}
	s, err := proxy.New(cfg,
		proxy.WithProjects(projects{p: compiled(t, res)}),
		proxy.WithWorkers(w),
		proxy.WithWarn(said.add),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s, said
}

func TestServeBindsEveryAddressOnAnAllocatedPort(t *testing.T) {
	s, _ := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, servable(), &workers{said: "pong:"})

	sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)

	require.NotZero(t, sock.Port, "the kernel allocated one")
	require.Equal(t, "postgres", sock.Protocol)
	require.Equal(t, "tcp", sock.Transport)
	require.True(t, strings.HasSuffix(sock.Host, ".tau.example.io"),
		"a generated name is completed by the cloud's own zone, got %q", sock.Host)
	require.True(t, strings.HasPrefix(sock.Host, "db."), "and keeps the label the author chose, got %q", sock.Host)

	require.Equal(t, []proxy.Socket{sock}, s.Serving(), "and that is what this node reports")
}

func TestAProxyAnswersOnV4AndV6(t *testing.T) {
	for _, dial := range []string{"127.0.0.1", "::1"} {
		t.Run(dial, func(t *testing.T) {
			w := &workers{said: "pong:"}
			s, _ := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, servable(), w)

			sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
			require.NoError(t, err)

			c, err := net.Dial("tcp", net.JoinHostPort(dial, strconv.Itoa(int(sock.Port))))
			if err != nil {
				t.Skipf("no %s on this machine: %s", dial, err)
			}
			defer c.Close()

			_, err = c.Write([]byte("ping"))
			require.NoError(t, err)
			require.NoError(t, c.(*net.TCPConn).CloseWrite())

			c.SetReadDeadline(time.Now().Add(2 * time.Second))
			got, err := io.ReadAll(c)
			require.NoError(t, err)
			require.Equal(t, "pong:ping", string(got))
		})
	}
}

func TestAGeneratedNameWithNoCloudZoneIsReportedNowhere(t *testing.T) {
	s, said := service(t, proxy.Config{}, servable(), &workers{})

	sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)
	require.NotZero(t, sock.Port)
	require.Empty(t, sock.Host)
	require.Empty(t, s.Serving(), "nothing is published for a name that cannot be spelled")
	require.Equal(t, 1, said.len(), "and the operator is told")
}

func TestADeclaredZoneIsCompleteOnItsOwn(t *testing.T) {
	res := servable()
	res["host"] = map[string]any{
		"kind": "zone", "name": "db",
		"zone": map[string]any{"in": map[string]any{"name": zoneID}},
	}
	s, _ := service(t, proxy.Config{}, res, &workers{})

	sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)
	require.Equal(t, "db.shop.example.com", sock.Host)
}

func TestServeRefusesBeforeItBinds(t *testing.T) {
	res := servable()
	res["tls"] = map[string]any{"mode": "terminate"}
	s, _ := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, res, &workers{})

	_, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.ErrorIs(t, err, proxy.ErrUnservable)
	require.Empty(t, s.Serving(), "and nothing is published")
}

func TestTheSrvLabelsFallBackToTheVocabularysDefaults(t *testing.T) {
	res := servable()
	delete(res, "service")
	s, _ := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, res, &workers{})

	sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)
	require.Equal(t, "pgwire", sock.Protocol, "the resource's own name, not the name it was looked up by")
	require.Equal(t, proxy.Transport, sock.Transport, "which the kind states, because no resource does")
}

func TestAProxyThatDeclaresNoHostPublishesNothing(t *testing.T) {
	res := servable()
	delete(res, "host")
	s, said := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, res, &workers{})

	sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.NoError(t, err)
	require.NotZero(t, sock.Port)
	require.Empty(t, sock.Host)
	require.Empty(t, s.Serving())
	require.Equal(t, 1, said.len())
}

func TestServeFailsWhenTheProjectCannotBeRead(t *testing.T) {
	s, err := proxy.New(proxy.Config{},
		proxy.WithProjects(projects{p: compiled(t, servable())}),
		proxy.WithWorkers(&workers{}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })

	_, err = s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "nosuch"})
	require.Error(t, err)
	require.Empty(t, s.Serving())
}

func TestAProxyThatForwardsToNothingClosesTheConnection(t *testing.T) {
	refusedBy(t, func(r map[string]any) { delete(r, "forward") })
}

func TestServeAfterCloseBindsNothing(t *testing.T) {
	s, _ := service(t, proxy.Config{CloudFqdn: "tau.example.io"}, servable(), &workers{})
	require.NoError(t, s.Close())

	sock, err := s.Serve(t.Context(), proxy.Bind{Project: "shop", Environment: "prod", Name: "pgwire"})
	require.Error(t, err)

	_, dialErr := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(sock.Port))), time.Second)
	require.Error(t, dialErr, "the socket it opened was closed with it")
	require.Empty(t, s.Serving())
}

// redeployable is a deployment whose compiled project changes under a running
// service, which is what a deploy is.
type redeployable struct {
	mu sync.Mutex
	p  *specs.Project
}

func (r *redeployable) set(p *specs.Project) {
	r.mu.Lock()
	r.p = p
	r.mu.Unlock()
}

func (r *redeployable) At(context.Context, register.Deployment) (*specs.Project, register.Pointer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.p, register.Pointer{}, nil
}
