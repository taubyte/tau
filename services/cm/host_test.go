package cm

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
)

// instance is what cm starts on: a peer, a directory, its config.
type instance struct {
	peer.Node
	*service.Served
	root   string
	config func(into any) error
}

func (i instance) Peer() peer.Node          { return i.Node }
func (instance) Running() []service.Running { return nil }
func (instance) Cloud() string              { return "cm-test" }
func (instance) Region() string             { return "" }
func (instance) Now() time.Time             { return time.Now() }
func (i instance) Root() string             { return i.root }
func (i instance) Config(into any) error {
	if i.config == nil {
		return nil
	}
	return i.config(into)
}

var _ service.Host = instance{}

func newInstance(t *testing.T, root string) instance {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	return instance{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond}), root: root}
}

func TestACloudWithNoZoneKeepsNoStore(t *testing.T) {
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newInstance(t, t.TempDir())))
	require.Nil(t, s.Store(), "nothing to keep certificates for")
	require.Nil(t, On(instance{}).Store(), "and none beside an instance running no cm")
	require.Nil(t, On(instance{}).Members(), "nor any node, with no directory to ask")

	i := newInstance(t, t.TempDir())
	i.config = func(into any) error {
		*into.(*hostConfig) = hostConfig{CM: Config{Certificate: CertificateConfig{ACME: pkgcertificate.ACME{Enabled: true}}}}
		return nil
	}
	require.ErrorIs(t, New().Start(t.Context(), i), ErrNoZone, "a cloud issuing certificates issues them for its zone")
}

// hosting is an instance with services beside cm.
type hosting struct {
	instance
	running []service.Running
}

func (h hosting) Running() []service.Running { return h.running }

// databases is an instance's shared databases, or one that cannot open any.
type databases struct{ err error }

func (d databases) Database(name string) (kvdb.Instance, error) {
	if d.err != nil {
		return nil, d.err
	}
	return kvdbmock.New().New(nil, name, 0)
}

func zoned(t *testing.T, cfg Config) instance {
	t.Helper()
	i := newInstance(t, t.TempDir())
	i.config = func(into any) error {
		*into.(*hostConfig) = hostConfig{CloudFqdn: "tau.example", CM: cfg}
		return nil
	}
	return i
}

func TestCMKeepsTheStoreWhereTheCloudNamesAZone(t *testing.T) {
	alone := hosting{instance: zoned(t, Config{})}
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), alone), "its databases its own, opened on the peer")
	require.NotNil(t, s.Store())
	alone.running = []service.Running{{Name: Name, Service: s}}
	require.Same(t, s.Store(), On(alone).Store(), "the store beside the instance")

	shared := hosting{instance: zoned(t, Config{}), running: []service.Running{{Service: databases{}}}}
	beside := New()
	t.Cleanup(func() { beside.Close() })
	require.NoError(t, beside.Start(t.Context(), shared), "the instance's databases")
	require.NotNil(t, beside.Store())

	broken := hosting{instance: zoned(t, Config{}), running: []service.Running{{Service: databases{err: errors.New("disk gone")}}}}
	require.ErrorContains(t, New().Start(t.Context(), broken), "disk gone")

	acme := pkgcertificate.ACME{Enabled: true}
	refused := hosting{instance: zoned(t, Config{Certificate: CertificateConfig{ACME: acme, Challenge: "tls-alpn-01"}})}
	require.Error(t, New().Start(t.Context(), refused), "a challenge it does not answer")
}
