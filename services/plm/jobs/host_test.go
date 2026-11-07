package jobs

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/raft"
)

// testHost is an instance as jobs sees it: a peer, a directory, the services
// beside it, and the section an operator wrote.
type testHost struct {
	peer.Node
	root    string
	running []service.Running
	section map[string]any
}

func (h *testHost) Peer() peer.Node            { return h.Node }
func (h *testHost) Running() []service.Running { return h.running }
func (h *testHost) Root() string               { return h.root }
func (*testHost) Cloud() string                { return "test" }
func (*testHost) Region() string               { return "" }
func (*testHost) Now() time.Time               { return time.Now() }

func (h *testHost) Config(into any) error {
	raw, err := yaml.Marshal(h.section)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}

// hostWithConsensus is a host whose instance opens groups, which jobs cannot start
// without.
func hostWithConsensus(t *testing.T, section map[string]any) *testHost {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &testHost{Node: p, root: t.TempDir(), section: section}
	h.running = []service.Running{{Service: groupsOn{node: p, root: h.root}}}
	return h
}

func TestTheBackendStartsFromItsInstance(t *testing.T) {
	h := hostWithConsensus(t, nil)
	b := New()
	require.ErrorIs(t, b.WaitForLeader(t.Context()), ErrNotStarted)
	require.NoError(t, b.Start(t.Context(), h))
	t.Cleanup(func() { b.Close() })

	require.NoError(t, b.WaitForLeader(t.Context()))
	_, err := b.Built("shop", "prod", "QmNothing")
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "leading its queue, nothing built is NotFound")

	require.ErrorContains(t, b.Start(t.Context(), h), "already started")
	require.NoError(t, b.Close())
}

func TestTheBackendNeedsConsensusBesideIt(t *testing.T) {
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	b := New()
	require.ErrorIs(t, b.Start(t.Context(), &testHost{Node: p, root: t.TempDir()}), ErrNoConsensus)
	require.NoError(t, b.Close(), "jobs that never started close")
}

func TestTheBackendRefusesASectionOfTheWrongShape(t *testing.T) {
	b := New()
	require.Error(t, b.Start(t.Context(), hostWithConsensus(t, map[string]any{"maxQueueDepth": "many"})))
	require.NoError(t, b.Close())
}

// groupsOn opens real groups on a test's peer, timed so a lone peer elects itself in
// milliseconds: the instance's groups, as a service beside it finds them.
type groupsOn struct {
	node peer.Node
	root string
}

func (g groupsOn) Group(namespace string) (raft.Cluster, error) {
	return raft.New(g.node, namespace, quickRaft.Options(g.root)...)
}

// quickRaft is raft timed for one process.
var quickRaft = raft.Timing{
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
