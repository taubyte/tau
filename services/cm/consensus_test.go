package cm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	pkgcertificate "github.com/taubyte/tau/pkg/certificate"
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
	require.Nil(t, StoreOn(instance{})(), "and none beside an instance running no cm")

	i := newInstance(t, t.TempDir(), quick)
	i.config = func(into any) error {
		*into.(*hostConfig) = hostConfig{CM: Config{Certificate: CertificateConfig{ACME: pkgcertificate.ACME{Enabled: true}}}}
		return nil
	}
	require.ErrorIs(t, New().Start(t.Context(), i), ErrNoZone, "a cloud issuing certificates issues them for its zone")
}
