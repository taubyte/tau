package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
)

// node is what a gateway starts on: a peer, what runs beside it, and its section.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
	config  func(*hostConfig) error
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "gateway-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Unix(1_700_000_000, 0) }
func (n *node) Config(into any) error {
	if n.config == nil {
		return nil
	}
	return n.config(into.(*hostConfig))
}

func newNode(t *testing.T, running ...any) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	n := &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond})}
	for _, r := range running {
		switch beside := r.(type) {
		case routes:
			n.Serve(statsService.Protocol, beside)
		case workerBeside:
			n.Serve(worker.Protocol, beside)
		default:
			n.running = append(n.running, service.Running{Service: r})
		}
	}
	return n
}

// routes is a stats view naming no node, served under stats' protocol.
type routes struct{}

func (routes) RoutePeers(string, time.Duration, []string) []string { return nil }
func (routes) Start(context.Context, service.Host) error           { return nil }
func (routes) Close() error                                        { return nil }

// workerBeside is the worker a gateway's node runs: who may attach, and where a
// build's artifact is.
type workerBeside struct {
	admitsBeside
	builtBeside
}

func (workerBeside) Start(context.Context, service.Host) error { return nil }
func (workerBeside) Close() error                              { return nil }

type busesBeside struct{}

func (busesBeside) Attach(context.Context, string) (Attachment, error) {
	return nil, errors.New("none")
}

type admitsBeside struct{}

func (admitsBeside) AdmitInstance(context.Context, string, map[string]string, string) error {
	return nil
}

func (admitsBeside) AdmitRequest(context.Context, specs.Address, *invoke.Request) (*invoke.Admission, error) {
	return nil, nil
}

type builtBeside struct{}

func (builtBeside) Artifact(context.Context, string, string, string, string) (string, error) {
	return "bafy", nil
}

type issuing struct {
	issuer Issuer
	err    error
}

func (i issuing) Issuer() (Issuer, error) { return i.issuer, i.err }

type oneIssuer struct{}

func (oneIssuer) Certificate(context.Context, string) (tls.Certificate, error) {
	return tls.Certificate{}, nil
}
func (oneIssuer) Challenge(string) (string, bool) { return "", false }

func TestAGatewayStartsFromItsNodeAndTakesWhatIsBesideIt(t *testing.T) {
	n := newNode(t, routes{}, busesBeside{}, workerBeside{}, issuing{issuer: oneIssuer{}})
	n.config = func(cfg *hostConfig) error {
		cfg.CloudFqdn = "tau.example.io"
		cfg.MaxBody = 1 << 10
		cfg.TLS = true
		return nil
	}
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), n))

	require.Equal(t, "tau.example.io", s.cfg.CloudFqdn, "the node's zone")
	require.Equal(t, int64(1<<10), s.cfg.MaxBody, "and its own section")
	require.NotNil(t, s.hosts)
	require.NotNil(t, s.workers)
	require.NotNil(t, s.secrets)
	require.Equal(t, busesBeside{}, s.buses)
	require.Equal(t, workerBeside{}, s.admits, "who may attach is the worker's beside it")
	require.Equal(t, workerBeside{}, s.artifacts, "and so is where a build's artifact is")
	require.NotNil(t, s.assets, "a package's files are read through storage")
	require.Equal(t, oneIssuer{}, s.issuer)
	require.True(t, s.terminates, "served over TLS, as its section says")
	require.Equal(t, time.Unix(1_700_000_000, 0), s.now(), "the node's clock")

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "close is idempotent")
}

func TestAGatewayAloneTakesOnlyWhatItMust(t *testing.T) {
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newNode(t, routes{}, issuing{})))
	require.Nil(t, s.buses)
	require.Nil(t, s.admits)
	require.Nil(t, s.artifacts)
	require.Nil(t, s.issuer, "a cloud that issues nothing")
	require.False(t, s.terminates)
}

func TestAGatewayRefusesWhatItCannotStartWith(t *testing.T) {
	start := func(n *node) error {
		s := New()
		t.Cleanup(func() { s.Close() })
		return s.Start(t.Context(), n)
	}
	require.ErrorContains(t, start(newNode(t)), "places requests from stats")
	require.ErrorContains(t, start(newNode(t, routes{}, issuing{err: errors.New("no account")})), "no account")

	broken := newNode(t, routes{})
	broken.config = func(*hostConfig) error { return errors.New("no such key") }
	require.ErrorContains(t, start(broken), "no such key")
}

func TestAGatewayNeverStartedClosesCleanly(t *testing.T) {
	require.NoError(t, New().Close())
}

var _ io.Closer = New()
