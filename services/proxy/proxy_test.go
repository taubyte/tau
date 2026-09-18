package proxy

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/stats"
)

// THE "NODES" HERE ARE REAL HTTP SERVERS. A fake round-tripper returning a
// canned response would test the branching and none of the forwarding, and the
// forwarding is where a header gets dropped or a body gets truncated.

type fakeRouter struct {
	mu       sync.Mutex
	nodes    map[string][]stats.Node
	lastName string
	lastAge  time.Duration
}

func (r *fakeRouter) Route(name string, maxAge time.Duration) []stats.Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastName, r.lastAge = name, maxAge
	return r.nodes[name]
}

func (r *fakeRouter) asked() (string, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastName, r.lastAge
}

func serving(name string, nodes ...stats.Node) *fakeRouter {
	return &fakeRouter{nodes: map[string][]stats.Node{name: nodes}}
}

// backend is one "node": a real server, plus a record of what reached it.
type backend struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits int
}

func newBackend(t *testing.T, h http.HandlerFunc) *backend {
	t.Helper()
	b := &backend{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.hits++
		b.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *backend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hits
}

func (b *backend) transport() http.RoundTripper {
	return &redirectTransport{to: b.srv.URL}
}

// redirectTransport sends whatever it is given to one server, which is what a
// p2p forwarder does in production: the request's Host names the workload, not
// the address it travels to.
type redirectTransport struct{ to string }

func (t *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	u := *r.URL
	target := strings.TrimPrefix(t.to, "http://")
	u.Scheme, u.Host = "http", target
	out.URL = &u
	out.RequestURI = ""
	return http.DefaultTransport.RoundTrip(out)
}

// byPeer forwards to whichever backend is registered for that peer.
func byPeer(m map[string]*backend) Forwarder {
	return ForwarderFunc(func(n stats.Node) (http.RoundTripper, error) {
		b, ok := m[n.Peer]
		if !ok {
			return nil, errors.New("no route to " + n.Peer)
		}
		return b.transport(), nil
	})
}

func newProxy(t *testing.T, r Router, f Forwarder, opts ...Option) *Service {
	t.Helper()
	s, err := New(Config{}, append([]Option{WithRouter(r), WithForwarder(f)}, opts...)...)
	require.NoError(t, err)
	return s
}

func request(host, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
	r.Host = host
	return r
}

// TestARequestReachesTheNodeStatsChose, headers and body intact.
func TestARequestReachesTheNodeStatsChose(t *testing.T) {
	b := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/checkout", r.URL.Path)
		require.Equal(t, "yes", r.Header.Get("X-Probe"))
		w.Header().Set("X-From", "the-node")
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "answered")
	})

	r := serving("shop.example.com", stats.Node{Peer: "n1", Ready: true})
	s := newProxy(t, r, byPeer(map[string]*backend{"n1": b}))

	req := request("shop.example.com", "/checkout")
	req.Header.Set("X-Probe", "yes")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	require.Equal(t, http.StatusTeapot, rec.Code)
	require.Equal(t, "answered", rec.Body.String())
	require.Equal(t, "the-node", rec.Header().Get("X-From"), "the node's headers reach the client")

	name, age := r.asked()
	require.Equal(t, "shop.example.com", name)
	require.Equal(t, DefaultMaxAge, age, "the proxy states its own staleness tolerance")
}

// TestTheLeastLoadedNodeIsTriedFirst — stats orders, the proxy obeys.
func TestTheLeastLoadedNodeIsTriedFirst(t *testing.T) {
	idle := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "idle") })
	busy := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "busy") })

	s := newProxy(t,
		serving("shop.example.com",
			stats.Node{Peer: "idle", Load: 0.1, Ready: true},
			stats.Node{Peer: "busy", Load: 0.9, Ready: true}),
		byPeer(map[string]*backend{"idle": idle, "busy": busy}))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, "idle", rec.Body.String())
	require.Equal(t, 1, idle.count())
	require.Zero(t, busy.count(), "the busy node was not touched")
}

// TestAFailureToConnectMovesToTheNextNode. This is the ONLY retry: nothing was
// sent, so nothing can run twice.
func TestAFailureToConnectMovesToTheNextNode(t *testing.T) {
	good := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "second") })

	s := newProxy(t,
		serving("shop.example.com",
			stats.Node{Peer: "gone", Ready: true},
			stats.Node{Peer: "good", Ready: true}),
		byPeer(map[string]*backend{"good": good})) // "gone" has no route

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "second", rec.Body.String())
	require.Equal(t, 1, good.count())
}

// TestANodeThatANSWERSEndsIt, whatever it answered. Retrying a 500 elsewhere
// would run the request twice and then call the second result the truth.
func TestANodeThatAnswersEndsIt(t *testing.T) {
	failing := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "the workload is unhappy", http.StatusInternalServerError)
	})
	other := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "never") })

	s := newProxy(t,
		serving("shop.example.com",
			stats.Node{Peer: "failing", Load: 0.1, Ready: true},
			stats.Node{Peer: "other", Load: 0.9, Ready: true}),
		byPeer(map[string]*backend{"failing": failing, "other": other}))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "the workload is unhappy",
		"the workload's own answer belongs to the client")
	require.Zero(t, other.count(), "and nothing was run a second time")
}

// TestTheAttemptCapHolds, so a large fleet with a systemic fault does not turn
// one client request into a sweep of every node.
func TestTheAttemptCapHolds(t *testing.T) {
	var nodes []stats.Node
	for i := range 10 {
		nodes = append(nodes, stats.Node{Peer: string(rune('a' + i)), Ready: true})
	}
	tried := 0
	f := ForwarderFunc(func(stats.Node) (http.RoundTripper, error) {
		tried++
		return nil, errors.New("unreachable")
	})

	s, err := New(Config{MaxAttempts: 3}, WithRouter(serving("shop.example.com", nodes...)), WithForwarder(f))
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, 3, tried, "three nodes, not ten")
}

// TestTheLocalNodeIsServedWithoutANetworkHop.
func TestTheLocalNodeIsServedWithoutANetworkHop(t *testing.T) {
	local := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local") })
	remote := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "remote") })

	forwarded := 0
	f := ForwarderFunc(func(n stats.Node) (http.RoundTripper, error) {
		forwarded++
		return remote.transport(), nil
	})

	s := newProxy(t,
		serving("shop.example.com", stats.Node{Peer: "me", Load: 0.1, Ready: true}),
		f, WithLocal("me", local.transport()))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, "local", rec.Body.String())
	require.Zero(t, forwarded, "the forwarder was never asked for a node on this machine")
}

// TestTheLocalNodeIsNotFavoured, only shortcut. The load ordering still decides
// who serves; being local just means not dialling out to reach it.
func TestTheLocalNodeIsNotFavoured(t *testing.T) {
	local := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "local") })
	idle := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "idle-remote") })

	s := newProxy(t,
		serving("shop.example.com",
			stats.Node{Peer: "other", Load: 0.1, Ready: true},
			stats.Node{Peer: "me", Load: 0.9, Ready: true}),
		byPeer(map[string]*backend{"other": idle}),
		WithLocal("me", local.transport()))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, "idle-remote", rec.Body.String(),
		"the idle remote node beat the busy local one")
	require.Zero(t, local.count())
}

// TestNothingServingIs503AndNever404. A name whose nodes are all restarting
// exists; 404 tells the client and every cache between that it is gone.
func TestNothingServingIs503AndNever404(t *testing.T) {
	s := newProxy(t, &fakeRouter{nodes: map[string][]stats.Node{}},
		ForwarderFunc(func(stats.Node) (http.RoundTripper, error) { return nil, nil }))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.NotEqual(t, http.StatusNotFound, rec.Code)
}

func TestNoNodeReachableIs502(t *testing.T) {
	s := newProxy(t,
		serving("shop.example.com", stats.Node{Peer: "gone", Ready: true}),
		ForwarderFunc(func(stats.Node) (http.RoundTripper, error) {
			return nil, errors.New("unreachable")
		}))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))
	require.Equal(t, http.StatusBadGateway, rec.Code)
}

// TestTheHostIsNormalisedBeforeItIsLookedUp. A client may send a port and any
// case; a workload is deployed under one name.
func TestTheHostIsNormalisedBeforeItIsLookedUp(t *testing.T) {
	b := newBackend(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	r := serving("shop.example.com", stats.Node{Peer: "n1", Ready: true})
	s := newProxy(t, r, byPeer(map[string]*backend{"n1": b}))

	for _, host := range []string{
		"shop.example.com",
		"shop.example.com:443",
		"SHOP.Example.COM",
		"SHOP.Example.COM:8080",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, request(host, "/"))
		require.Equal(t, http.StatusOK, rec.Code, host)

		name, _ := r.asked()
		require.Equal(t, "shop.example.com", name, host)
	}
}

func TestARequestWithNoHostIsRefused(t *testing.T) {
	s := newProxy(t, serving("x"), ForwarderFunc(func(stats.Node) (http.RoundTripper, error) {
		return nil, nil
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestTheResponseBodyIsNotTruncated. The request's context is cancelled when
// the body is closed, not when RoundTrip returns — cancelling early closes a
// streaming body under the reader and looks like a short read.
func TestTheResponseBodyIsNotTruncated(t *testing.T) {
	big := strings.Repeat("x", 512<<10)
	b := newBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		for range 8 {
			io.WriteString(w, big)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})

	s := newProxy(t, serving("shop.example.com", stats.Node{Peer: "n1", Ready: true}),
		byPeer(map[string]*backend{"n1": b}))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, request("shop.example.com", "/"))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 8*len(big), rec.Body.Len(), "the whole streamed body arrived")
}

func TestLoggingSaysWhatCouldNotBeReached(t *testing.T) {
	var lines []string
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, format)
	}

	s := newProxy(t, &fakeRouter{nodes: map[string][]stats.Node{}},
		ForwarderFunc(func(stats.Node) (http.RoundTripper, error) { return nil, nil }),
		WithLogger(logf))
	s.ServeHTTP(httptest.NewRecorder(), request("shop.example.com", "/"))

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, lines, "a refusal a client sees as 503 needs a reason somewhere")
}

func TestConfigAndOptionsAreValidated(t *testing.T) {
	r := serving("x")
	f := ForwarderFunc(func(stats.Node) (http.RoundTripper, error) { return nil, nil })

	_, err := New(Config{}, WithForwarder(f))
	require.ErrorIs(t, err, ErrNoRouter)

	_, err = New(Config{}, WithRouter(r))
	require.ErrorIs(t, err, ErrNoForwarder)

	_, err = New(Config{}, WithRouter(nil))
	require.ErrorIs(t, err, ErrNoRouter)

	_, err = New(Config{}, WithForwarder(nil))
	require.ErrorIs(t, err, ErrNoForwarder)

	for _, cfg := range []Config{
		{MaxAge: -time.Second}, {MaxAttempts: -1}, {Timeout: -time.Second},
	} {
		_, err := New(cfg, WithRouter(r), WithForwarder(f))
		require.Error(t, err)
	}

	_, err = New(Config{}, WithRouter(r), WithForwarder(f), WithLocal("", nil))
	require.Error(t, err, "a peer id with no way to serve it routes local requests into nothing")

	_, err = New(Config{}, WithRouter(r), WithForwarder(f), WithLogger(nil))
	require.Error(t, err)

	s, err := New(Config{}, WithRouter(r), WithForwarder(f))
	require.NoError(t, err)
	require.Equal(t, DefaultMaxAge, s.cfg.MaxAge)
	require.Equal(t, DefaultMaxAttempts, s.cfg.MaxAttempts)
	require.Equal(t, DefaultTimeout, s.cfg.Timeout)
}
