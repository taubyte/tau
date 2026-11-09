package primitives

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/register"
)

// node is the instance primitives runs on: a peer, a directory, a section per
// service, and what it runs.
type node struct {
	peer.Node
	*service.Served
	root     string
	sections map[string]func(into any) error

	mu      sync.Mutex
	running []service.Running
}

// on is node as the service called name sees it.
type on struct {
	*node
	name string
}

func (o on) Peer() peer.Node { return o.Node }
func (o on) Running() []service.Running {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.running)
}
func (o on) Config(into any) error {
	if section := o.sections[o.name]; section != nil {
		return section(into)
	}
	return nil
}
func (o on) Root() string { return o.root }
func (on) Cloud() string  { return "primitives-test" }
func (on) Region() string { return "" }
func (on) Now() time.Time { return time.Now() }

var _ service.Host = on{}

func newNode(t *testing.T) *node {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	return &node{Node: p, Served: service.NewServed(p.ID(), nil, rpc.Retry{For: 50 * time.Millisecond}), root: t.TempDir(), sections: map[string]func(any) error{}}
}

func (n *node) beside(name string, s any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.running = append(n.running, service.Running{Name: name, Service: s})
}

func (n *node) start(t *testing.T, name string, s service.Service) {
	t.Helper()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), on{node: n, name: name}))
	n.beside(name, s)
}

func TestPrimitivesRunsFromWhatItsInstanceGivesIt(t *testing.T) {
	ctx := t.Context()
	n := newNode(t)
	n.beside("", groupsOn{node: n.Node, root: n.root})
	reg := register.New()
	n.start(t, register.Descriptor.Name, reg)
	_, err := reg.Local().Publish(ctx, pkgregister.Publication{Project: projectID, Environment: "prod", Revision: 1, Compiled: compiled()})
	require.NoError(t, err)
	n.beside("", workloadsOf{
		of: func(context.Context) []Schedule { return nil },
		serve: func(context.Context, Target, *invoke.Request) (*invoke.Response, error) {
			return &invoke.Response{Status: 200}, nil
		},
	})

	p := New()
	n.start(t, Descriptor.Name, p)
	require.ErrorIs(t, New().WaitForLeader(ctx), ErrForming, "a service never started has no group")
	require.NoError(t, p.WaitForLeader(ctx))

	// ITS GROUP IS THE CONSENSUS SERVICE'S, timed as that service was told, and its
	// queue is the one the register beside it declares.
	require.NoError(t, p.Local().Send(ctx, grantFor(jobsID), "a", []byte("job")))
	depth, err := p.Local().Depth(ctx, grantFor(jobsID))
	require.NoError(t, err)
	require.Equal(t, uint64(1), depth.Ready)

	// ITS RECORDS ARE IN A DATABASE OF ITS OWN on the peer.
	_, err = p.Local().Append(ctx, grantFor(auditID), []byte("line"))
	require.NoError(t, err)
	read, err := p.Local().Read(ctx, grantFor(auditID), "", 10)
	require.NoError(t, err)
	require.Len(t, read, 1)

	// A WINDOW WITH NO DIRECTORY TO ROUTE IT goes to this node, which fires through
	// the workloads beside it.
	s := p.(*server)
	_, clocked := s.workers()
	require.True(t, clocked)
	require.NoError(t, s.task(ctx, time.Now().Add(-time.Minute), time.Now()))
}

func TestPrimitivesRefusesWhatItCannotStartWith(t *testing.T) {
	n := newNode(t)
	require.ErrorIs(t, New().Start(t.Context(), on{node: n, name: Descriptor.Name}), ErrNoConsensus)

	n.sections[Descriptor.Name] = func(any) error { return errors.New("no such key") }
	require.ErrorContains(t, New().Start(t.Context(), on{node: n, name: Descriptor.Name}), "no such key")

	// A DEAD LETTER BOUND THE OPERATOR SET is the one kept.
	n.beside("", groupsOn{node: n.Node, root: n.root})
	n.sections[Descriptor.Name] = func(into any) error {
		*into.(*Config) = Config{Sweep: time.Hour, DeadLetter: DeadLetterConfig{Age: time.Hour, Size: 10}}
		return nil
	}
	p := New()
	n.start(t, Descriptor.Name, p)
	s := p.(*server)
	require.Equal(t, retention{age: time.Hour, size: 10}, s.dead)
	require.Equal(t, time.Hour, s.sweep)
	_, clocked := s.workers()
	require.False(t, clocked, "nothing beside it runs workloads")
}

func TestTheClockIsDialledAsItsTasking(t *testing.T) {
	a, b := newNode(t), newNode(t)
	c, err := dialClocks(a.Host(), b.ID())
	require.NoError(t, err)
	c.(*Client).Close()
}

// groupsOn opens real groups on a test's peer, timed so a lone peer elects itself in
// milliseconds: the instance's groups, as a service beside it finds them.
type groupsOn struct {
	node peer.Node
	root string
}

func (g groupsOn) Group(namespace string) (raft.Cluster, error) {
	return raft.New(g.node, namespace, quickRaft.Options()...)
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
