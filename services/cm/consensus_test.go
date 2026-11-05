package cm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
	kvdbmock "github.com/taubyte/tau/pkg/kvdb/mock"
	"github.com/taubyte/tau/pkg/raft"
)

// instance is what cm starts on: a peer, a directory, its section.
type instance struct {
	peer.Node
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

func newInstance(t *testing.T, root string, cfg ConsensusConfig) instance {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	return instance{Node: p, root: root, config: func(into any) error {
		*into.(*hostConfig) = hostConfig{Section: Section{Consensus: cfg}}
		return nil
	}}
}

// quick is raft timed for one node in one process.
var quick = ConsensusConfig{
	Timeouts: raft.TimeoutConfig{
		HeartbeatTimeout:   50 * time.Millisecond,
		ElectionTimeout:    50 * time.Millisecond,
		CommitTimeout:      25 * time.Millisecond,
		LeaderLeaseTimeout: 25 * time.Millisecond,
		SnapshotInterval:   time.Minute,
		SnapshotThreshold:  1000,
	},
	BootstrapTimeout: 200 * time.Millisecond,
}

func TestAGroupIsOpenedOnTheInstanceWithItsTiming(t *testing.T) {
	root := t.TempDir()
	i := newInstance(t, root, quick)
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), i))

	c, err := s.Raft("orders")
	require.NoError(t, err)
	require.Equal(t, "orders", c.Namespace())

	// LEADER WELL INSIDE raft's 30s default bootstrap: the section's timing is the
	// group's.
	require.Eventually(t, c.IsLeader, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, c.Set("k", []byte("v"), time.Second))
	got, ok := c.Get("k")
	require.True(t, ok)
	require.Equal(t, "v", string(got))

	_, err = os.Stat(filepath.Join(root, "raft", "orders"))
	require.NoError(t, err, "the group's snapshots are under the instance's directory")

	_, err = s.Raft("")
	require.ErrorContains(t, err, "cm: ")
}

func TestClosingCMClosesItsGroups(t *testing.T) {
	s := New()
	require.NoError(t, s.Start(t.Context(), newInstance(t, t.TempDir(), quick)))

	left, err := s.Raft("left-open")
	require.NoError(t, err)
	closed, err := s.Raft("closed-by-its-caller")
	require.NoError(t, err)
	require.NoError(t, closed.Close())

	require.NoError(t, s.Close(), "a group its caller closed is no failure")
	require.ErrorIs(t, left.Close(), raft.ErrAlreadyClosed, "the service closed what was still open")

	_, err = s.Raft("after")
	require.ErrorIs(t, err, ErrClosed)
}

func TestAGroupIsRefusedByName(t *testing.T) {
	_, err := New().Raft("early")
	require.ErrorIs(t, err, ErrNotStarted)

	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newInstance(t, "", quick)), "an instance with no directory still runs cm")
	_, err = s.Raft("rootless")
	require.ErrorIs(t, err, ErrNoRoot)

	i := newInstance(t, t.TempDir(), quick)
	i.config = func(any) error { return errors.New("no such key") }
	require.ErrorContains(t, New().Start(t.Context(), i), "no such key")
}

func TestRaftOptionsSayOnlyWhatIsSet(t *testing.T) {
	require.Len(t, RaftOptions("/r", ConsensusConfig{}), 1, "the snapshot root alone: raft's own timing")
	require.Len(t, RaftOptions("/r", quick), 3)
}

func TestACloudWithNoZoneKeepsNoStore(t *testing.T) {
	s := New()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), newInstance(t, t.TempDir(), quick)))
	require.Nil(t, s.Store(), "nothing to keep certificates for")
	require.Nil(t, On(instance{}).Local(), "and none beside an instance running no cm")
	require.Nil(t, On(instance{}).Members(), "nor any node, with no directory to ask")

	i := newInstance(t, t.TempDir(), quick)
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

type answering struct{}

func (answering) LookupTXT(context.Context, string) ([]string, error) { return nil, nil }

func zoned(t *testing.T, cfg Config) instance {
	t.Helper()
	i := newInstance(t, t.TempDir(), quick)
	i.config = func(into any) error {
		*into.(*hostConfig) = hostConfig{CloudFqdn: "tau.example", CM: cfg, Section: Section{Consensus: quick}}
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
	require.Same(t, s.Store(), On(alone).Local(), "the store beside the instance")

	shared := hosting{instance: zoned(t, Config{}), running: []service.Running{{Service: databases{}}, {Service: answering{}}}}
	beside := New()
	t.Cleanup(func() { beside.Close() })
	require.NoError(t, beside.Start(t.Context(), shared), "the instance's databases, and the resolver it was handed")
	require.NotNil(t, beside.Store())

	broken := hosting{instance: zoned(t, Config{}), running: []service.Running{{Service: databases{err: errors.New("disk gone")}}}}
	require.ErrorContains(t, New().Start(t.Context(), broken), "disk gone")

	acme := pkgcertificate.ACME{Enabled: true}
	refused := hosting{instance: zoned(t, Config{Certificate: CertificateConfig{ACME: acme, Challenge: "tls-alpn-01"}})}
	require.Error(t, New().Start(t.Context(), refused), "a challenge it does not answer")
}
