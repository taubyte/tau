package gateway

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/certificate"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/cm"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
)

// node is what a gateway starts on: a peer, what runs beside it, and its section.
type node struct {
	peer.Node
	*service.Served
	running []service.Running
	config  func(*hostConfig) error
	// cm is the cloud-wide `cm:` block every section on a node carries.
	cm cm.Config
}

func (n *node) Peer() peer.Node            { return n.Node }
func (n *node) Running() []service.Running { return n.running }
func (*node) Root() string                 { return "" }
func (*node) Cloud() string                { return "gateway-test" }
func (*node) Region() string               { return "" }
func (*node) Now() time.Time               { return time.Unix(1_700_000_000, 0) }

// Config is the gateway's section as the test sets it, decoded into whatever reads it
// by yaml as a node decodes it: the gateway, and cm's helpers.
func (n *node) Config(into any) error {
	if err := roundTrip(cloudWide{CM: n.cm}, into); err != nil {
		return err
	}
	var cfg hostConfig
	if n.config != nil {
		if err := n.config(&cfg); err != nil {
			return err
		}
	}
	return roundTrip(cfg, into)
}

// cloudWide is what a node decodes into every section before the section's own.
type cloudWide struct {
	CM cm.Config `yaml:"cm,omitempty"`
}

func roundTrip(from, into any) error {
	raw, err := yaml.Marshal(from)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
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

// issuing is a cloud that issues certificates.
var issuing = cm.Config{Certificate: cm.CertificateConfig{ACME: certificate.ACME{Enabled: true}}}

func TestAGatewayStartsFromItsNodeAndTakesWhatIsBesideIt(t *testing.T) {
	n := newNode(t, routes{}, busesBeside{}, workerBeside{})
	n.cm = issuing
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
	require.IsType(t, &cm.Client{}, s.issuer, "a generated name is ordered through its own cm client")
	require.True(t, s.terminates, "served over TLS, as its section says")
	require.Equal(t, time.Unix(1_700_000_000, 0), s.now(), "the node's clock")

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "close is idempotent")
}

func TestAGatewayAloneTakesOnlyWhatItMust(t *testing.T) {
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newNode(t, routes{})))
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
	unzoned := newNode(t, routes{})
	unzoned.cm = issuing
	require.ErrorIs(t, start(unzoned), cm.ErrNoZone, "a cloud issuing certificates names the zone they are for")

	broken := newNode(t, routes{})
	broken.config = func(*hostConfig) error { return errors.New("no such key") }
	require.ErrorContains(t, start(broken), "no such key")
}

func TestAGatewayNeverStartedClosesCleanly(t *testing.T) {
	require.NoError(t, New().Close())
}

var _ io.Closer = New()
