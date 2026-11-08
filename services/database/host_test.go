package database

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/raft"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/services/register"
	statsService "github.com/taubyte/tau/services/stats"
)

// machine is the instance the database service runs on: a peer, a directory, a
// section per service, and what runs beside it.
type machine struct {
	peer.Node
	root     string
	sections map[string]func(into any) error

	mu      sync.Mutex
	running []service.Running
}

// as is machine as the service called name sees it.
type as struct {
	*machine
	name string
}

func (a as) Peer() peer.Node { return a.Node }
func (a as) Running() []service.Running {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.running)
}
func (a as) Config(into any) error {
	if section := a.sections[a.name]; section != nil {
		return section(into)
	}
	return nil
}
func (a as) Root() string { return a.root }
func (as) Cloud() string  { return "database-test" }
func (as) Region() string { return "" }
func (as) Now() time.Time { return time.Now() }

var _ service.Host = as{}

func newMachine(t *testing.T) *machine {
	t.Helper()
	p := peer.Mock(t.Context())
	t.Cleanup(p.Close)
	return &machine{Node: p, root: t.TempDir(), sections: map[string]func(any) error{}}
}

func (m *machine) beside(name string, s any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = append(m.running, service.Running{Name: name, Service: s})
}

func (m *machine) start(t *testing.T, name string, s service.Service) {
	t.Helper()
	t.Cleanup(func() { s.Close() })
	require.NoError(t, s.Start(t.Context(), as{machine: m, name: name}))
	m.beside(name, s)
}

// fleetOf is a stats service that sees the members it is given.
type fleetOf []placement.Member

func (f fleetOf) Placement() []placement.Member { return f }

func TestTheDatabaseServiceRunsFromWhatItsInstanceGivesIt(t *testing.T) {
	ctx := t.Context()
	m := newMachine(t)
	m.beside("", groupsOn{node: m.Node, root: m.root})
	reg := register.New()
	m.start(t, register.Descriptor.Name, reg)
	_, err := reg.Local().Publish(ctx, pkgregister.Publication{Project: projectID, Environment: "prod", Revision: 1, Compiled: compiled()})
	require.NoError(t, err)
	m.beside("stats", fleetOf{{ID: m.ID().String(), Accepts: true}})

	d := New()
	m.start(t, Descriptor.Name, d)

	// A KV DATABASE IS A REPLICATED STORE OF ITS OWN on the peer, and one it holds.
	kv, err := d.Local().OpenKV(ctx, sessionsGrant())
	require.NoError(t, err)
	require.NoError(t, kv.Put(ctx, "session:1", []byte("alice")))
	got, err := kv.Get(ctx, "session:1")
	require.NoError(t, err)
	require.Equal(t, []byte("alice"), got)
	require.NoError(t, kv.Delete(ctx, "session:1"))
	_, err = kv.Get(ctx, "session:1")
	require.Error(t, err)
	require.Equal(t, 1, d.Held())

	s := d.(*server)
	require.Len(t, s.holdings.OnDisk(ctx), 1, "what it wrote is on the peer's disk")

	// A LEDGER IS A GROUP OF THE CONSENSUS BESIDE IT.
	ledger, err := d.Local().OpenKV(ctx, ledgerGrant())
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ledger.Put(ctx, "entry-1", []byte("one")) == nil }, 10*time.Second, 20*time.Millisecond)

	// AND WHAT IT ADDS TO ITS NODE'S REPORT.
	var snap statsService.Snapshot
	s.Report(&snap)
	require.Equal(t, s.FleetRevision(), snap.FleetRevision)
	require.Equal(t, s.Pending(), snap.DatabasesPending)
}

func TestTheDatabaseServiceRefusesWhatItCannotStartWith(t *testing.T) {
	m := newMachine(t)
	require.ErrorIs(t, New().Start(t.Context(), as{machine: m, name: Descriptor.Name}), ErrNoConsensus)

	m.sections[Descriptor.Name] = func(any) error { return errors.New("no such key") }
	require.ErrorContains(t, New().Start(t.Context(), as{machine: m, name: Descriptor.Name}), "no such key")

	m.beside("", groupsOn{node: m.Node, root: m.root})
	m.sections[Descriptor.Name] = func(into any) error {
		*into.(*Config) = Config{Sweep: time.Hour, MaxResident: 3, Idle: time.Minute}
		return nil
	}
	d := New()
	m.start(t, Descriptor.Name, d)
	s := d.(*server)
	require.Equal(t, time.Hour, s.sweep)
	require.Empty(t, s.members(), "no stats beside it, no fleet")
}

func TestAStoreThisNodeReleasesIsDestroyedAndReopened(t *testing.T) {
	ctx := t.Context()
	m := newMachine(t)
	r := &replicas{Node: m.Node, fleet: nothingPublished{}, open: map[string]kvdb.Instance{}}
	t.Cleanup(func() { r.Close() })

	store, err := r.Store("databases/shop/prod/kv/QmA")
	require.NoError(t, err)
	require.NoError(t, store.Put(ctx, "k", []byte("v")))
	_, found, err := store.Get(ctx, "absent")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, store.Delete(ctx, "absent"), "deleting what is not there is nothing")
	keys, err := store.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, keys, 1)

	held, err := r.Holding(ctx, "databases/shop/prod/kv/QmA")
	require.NoError(t, err)
	require.True(t, held)
	paths, err := r.Paths(ctx, Prefix, 4)
	require.NoError(t, err)
	require.Equal(t, []string{"databases/shop/prod/kv/QmA"}, paths)

	require.NoError(t, r.Release(ctx, "databases/shop/prod/kv/QmA"))
	again, err := r.Store("databases/shop/prod/kv/QmA")
	require.NoError(t, err)
	_, found, err = again.Get(ctx, "k")
	require.NoError(t, err)
	require.False(t, found, "a released copy is gone")
}

// nothingPublished is a register with no fleet document.
type nothingPublished struct{}

func (nothingPublished) ResolveFleet(context.Context) (pkgregister.Pointer, error) {
	return pkgregister.Pointer{}, pkgregister.ErrNotPublished
}

func (nothingPublished) Object(context.Context, pkgregister.ObjectID) ([]byte, error) {
	return nil, pkgregister.ErrNotPublished
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
