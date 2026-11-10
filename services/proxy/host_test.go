package proxy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
)

// node is what a proxy starts on: a peer, what runs beside it, and its section.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
	config  func(*hostConfig) error
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "proxy-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Now() }
func (n *node) Config(into any) error {
	cfg := into.(*hostConfig)
	cfg.CloudFqdn = "tau.example.io"
	cfg.Sweep = 20 * time.Millisecond
	if n.config == nil {
		return nil
	}
	return n.config(cfg)
}

func newNode(t *testing.T, running ...any) *node {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	for _, r := range running {
		switch beside := r.(type) {
		case routes:
			n.Serve(statsService.Protocol, beside)
		case holding:
			n.Serve(worker.Protocol, beside)
		default:
			n.running = append(n.running, service.Running{Service: r})
		}
	}
	return n
}

// holding is the worker beside a proxy: a project and the proxies in it.
type holding struct {
	projects
	held []worker.Proxy
}

func (h holding) Proxies(context.Context) []worker.Proxy  { return h.held }
func (holding) Start(context.Context, service.Host) error { return nil }
func (holding) Close() error                              { return nil }

// routes is a stats view naming no node.
type routes struct{}

func (routes) RoutePeers(string, time.Duration, []string) []string { return nil }
func (routes) Start(context.Context, service.Host) error           { return nil }
func (routes) Close() error                                        { return nil }

func TestAProxyStartsFromItsNodeAndServesWhatTheWorkerHolds(t *testing.T) {
	held := holding{projects: projects{p: compiled(t, servable())}, held: []worker.Proxy{{Project: "shop", Environment: "prod", Name: "pgwire"}}}
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newNode(t, held, routes{})))

	require.Eventually(t, func() bool { return len(s.Serving()) == 1 }, 5*time.Second, 10*time.Millisecond, "the sweep Start runs binds what the worker holds")
	sock := s.Serving()[0]
	require.True(t, strings.HasSuffix(sock.Host, ".tau.example.io"), "named in the cloud's zone, got %q", sock.Host)

	var snap statsService.Snapshot
	s.Report(&snap)
	require.Equal(t, []string{sock.Host}, snap.Serving, "the name a client resolves first")
	require.Len(t, snap.Sockets, 1)
	require.Equal(t, sock.Port, snap.Sockets[0].Port)

	require.NoError(t, s.Close())
	require.Empty(t, s.Serving(), "closing stops the sweep and lets go of every socket")
	require.NoError(t, s.Close(), "close is idempotent")
}

func TestAProxyRefusesWhatItCannotStartWith(t *testing.T) {
	held := holding{projects: projects{p: compiled(t, servable())}}
	start := func(n *node) error {
		s := New()
		t.Cleanup(func() { s.Close() })
		return s.Start(t.Context(), n)
	}
	require.ErrorIs(t, start(newNode(t, routes{})), ErrNoWorker)
	require.ErrorContains(t, start(newNode(t, held)), "through placement", "no stats beside it")

	broken := newNode(t, held, routes{})
	broken.config = func(*hostConfig) error { return errors.New("no such key") }
	require.ErrorContains(t, start(broken), "no such key")
}
