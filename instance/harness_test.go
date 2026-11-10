package instance

import (
	"context"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"path"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-log/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/peer/peertest"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/raft"
	dnsService "github.com/taubyte/tau/services/dns"
)

// A node is tested with real services and fake resources: a mocknet host, gossipsub
// over it, an in-memory kvdb and a recording tier.

func newHost(t testing.TB, mn mocknet.Mocknet) host.Host {
	t.Helper()
	h, err := mn.GenPeer()
	require.NoError(t, err)
	require.NoError(t, mn.LinkAll())
	return h
}

func newPubSub(t testing.TB, h host.Host) *pubsub.PubSub {
	t.Helper()
	ps, err := pubsub.NewGossipSub(t.Context(), h)
	require.NoError(t, err)
	return ps
}

// memFactory is a kvdb.Factory over maps. The register's own tests prove what a
// real one does; what matters here is that one is asked for and used.
type memFactory struct {
	mu     sync.Mutex
	opened []string
	closed int
	err    error

	// byPath is one store per NAME, which is what a real factory gives: a
	// database is named, and opening it twice is opening the same rows. A fake
	// that handed back a fresh map each time would hide every place two
	// consumers are meant to see each other's writes.
	byPath map[string]*memKV
}

func (f *memFactory) New(_ log.StandardLogger, path string, _ int) (kvdb.Instance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}
	f.opened = append(f.opened, path)

	if f.byPath == nil {
		f.byPath = map[string]*memKV{}
	}
	if held, ok := f.byPath[path]; ok {
		return held, nil
	}
	db := &memKV{rows: map[string][]byte{}, factory: f}
	f.byPath[path] = db
	return db, nil
}

func (f *memFactory) Close() {}

func (f *memFactory) counts() (opened []string, closed int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...), f.closed
}

type memKV struct {
	kvdb.Instance
	factory *memFactory

	mu   sync.Mutex
	rows map[string][]byte

	// listErr is a store that answers reads but cannot scan: what a listing
	// over a failing database looks like to whoever asked for it.
	listErr error
}

func (k *memKV) Get(_ context.Context, key string) ([]byte, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.rows[datastoreKey(key)]
	if !ok {
		return nil, ds.ErrNotFound
	}
	return v, nil
}

// List is a prefix scan answering WHOLE KEYS, in the shape a real kvdb answers
// with — see datastoreKey.
func (k *memKV) List(_ context.Context, prefix string) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.listErr != nil {
		return nil, k.listErr
	}

	at := datastoreKey(prefix)
	var out []string
	for key := range k.rows {
		if strings.HasPrefix(key, at) {
			out = append(out, key)
		}
	}
	return out, nil
}

// datastoreKey does what pkg/kvdb does to every key: make it absolute and path-clean
// it. The double must too, or listed keys lack the leading slash and `..` is a
// distinct entry, hiding the bugs a real store exposes.
func datastoreKey(key string) string {
	cleaned := path.Clean("/" + key)
	if cleaned == "/" {
		return "/"
	}
	// A prefix keeps its trailing separator, which is what makes a prefix scan
	// a scan of one bucket rather than of every sibling that starts the same.
	if strings.HasSuffix(key, "/") {
		return cleaned + "/"
	}
	return cleaned
}

func (k *memKV) Put(_ context.Context, key string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rows[datastoreKey(key)] = v
	return nil
}

func (k *memKV) Delete(_ context.Context, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	at := datastoreKey(key)
	if _, ok := k.rows[at]; !ok {
		return ds.ErrNotFound
	}
	delete(k.rows, at)
	return nil
}

// Batch applies its writes on Commit, all at once.
func (k *memKV) Batch(context.Context) (kvdb.Batch, error) { return &memBatch{kv: k}, nil }

type memBatch struct {
	kv   *memKV
	puts map[string][]byte
	dels []string
}

func (b *memBatch) Put(key string, v []byte) error {
	if b.puts == nil {
		b.puts = map[string][]byte{}
	}
	b.puts[key] = v
	return nil
}

func (b *memBatch) Delete(key string) error {
	b.dels = append(b.dels, key)
	return nil
}

func (b *memBatch) Commit() error {
	b.kv.mu.Lock()
	defer b.kv.mu.Unlock()
	for key, v := range b.puts {
		b.kv.rows[datastoreKey(key)] = v
	}
	for _, key := range b.dels {
		delete(b.kv.rows, datastoreKey(key))
	}
	return nil
}

func (k *memKV) Close() {
	k.factory.mu.Lock()
	k.factory.closed++
	k.factory.mu.Unlock()
}

// opensDatabases is what a test's database double does.
type opensDatabases interface {
	New(logger log.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.Instance, error)
}

// withKVDB and withRaft open what the instance would open over its peer from
// something a test can see into or make fail.
func withKVDB(f opensDatabases) Option {
	return func(o *options) error {
		o.opener = factoryOpener{f}
		return nil
	}
}

// factoryOpener is a test's database double as what the instance opens through.
type factoryOpener struct{ f opensDatabases }

func (o factoryOpener) Open(name string) (kvdb.Instance, error) { return o.f.New(nil, name, 0) }

// overDouble is an instance built of nothing but p and databases opened from f.
func overDouble(t *testing.T, f opensDatabases) *Instance {
	t.Helper()
	p := mockPeer(t)
	return &Instance{Node: p, dbs: newDatabaseSet(p, factoryOpener{f})}
}

func withRaft(g raft.Groups) Option {
	return func(o *options) error {
		o.groups = g
		return nil
	}
}

// mockGroups opens a fresh group of one, in memory, for every namespace.
type mockGroups struct{}

func (mockGroups) Group(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }

// oneGroup answers every namespace with the same group.
type oneGroup struct{ raft.Cluster }

func (g oneGroup) Group(string) (raft.Cluster, error) { return g.Cluster, nil }

// mockRaft is a raft group of one, in memory. What a composition is asked to prove is
// the wiring; that raft replicates is pkg/raft's own test.
func mockRaft() Option {
	return withRaft(mockGroups{})
}

// mockPeer is a peer for one test, on the process's mocknet.
// callerOf is a host of another peer, linked and connected to p, to call what p
// serves.
func callerOf(t testing.TB, p peer.Node) host.Host {
	t.Helper()
	caller := mockPeer(t)
	require.NoError(t, peertest.LinkAll(t))
	require.NoError(t, caller.Host().Connect(t.Context(), libp2ppeer.AddrInfo{ID: p.ID(), Addrs: p.Host().Addrs()}))
	return caller.Host()
}

func mockPeer(t testing.TB) peer.Node {
	t.Helper()
	p := peertest.Mock(t, t.Context())
	t.Cleanup(p.Close)
	return p
}

// loopbackDNS is cfg with its dns section, on a node that runs dns, answering on an
// ephemeral loopback port unless the test named one: :53 is the operator's default,
// needs privilege, and a fleet needs a different port per node.
func loopbackDNS(cfg Config) Config {
	if len(cfg.Services) != 0 && !cfg.runs(DNS) {
		return cfg
	}
	cfg.Sections = maps.Clone(cfg.Sections)
	if cfg.Sections == nil {
		cfg.Sections = map[string]any{}
	}
	section, _ := cfg.Sections[DNS].(dnsService.Config)
	if section.Listen == "" {
		section.Listen = "127.0.0.1:0"
	}
	cfg.Sections[DNS] = section
	return cfg
}

// rooted is cfg with a directory of the test's own and, on a DNS node, an
// ephemeral loopback port: :53 needs privilege and would answer for the
// developer's resolver. An empty Services list means All, so ask as
// withDefaults does.
func rooted(t testing.TB, cfg Config) Config {
	t.Helper()
	if cfg.Root == "" {
		cfg.Root = t.TempDir()
	}
	cfg = loopbackDNS(cfg)
	// RAFT TIMED FOR ONE PROCESS, as dream times it: by default a group of one waits
	// out a 30s bootstrap before electing itself.
	if cfg.Raft == (raft.Timing{}) {
		cfg.Raft = quickRaft
	}
	// AND CLIENTS THAT GIVE UP FAST, since a test asking for a service nobody runs
	// expects the refusal, not half a minute of waiting for one.
	if cfg.Clients.Retry == (rpc.Retry{}) {
		cfg.Clients.Retry = rpc.Retry{First: 5 * time.Millisecond, Max: 20 * time.Millisecond, For: 100 * time.Millisecond}
	}
	return cfg
}

// quickRaft is raft timed for nodes one process apart.
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

// rootedIn is rooted, in the directory a test named.
func rootedIn(t testing.TB, cfg Config, root string) Config {
	t.Helper()
	cfg.Root = root
	return rooted(t, cfg)
}

// makeNode is an instance over a fresh peer, its databases a factory the test can
// see into and its raft groups in memory, not started.
func makeNode(t *testing.T, cfg Config, extra ...Option) (*Instance, host.Host, *memFactory) {
	t.Helper()
	p := mockPeer(t)
	factory := &memFactory{}
	opts := append([]Option{withKVDB(factory), mockRaft()}, extra...)
	n, err := New(p, rooted(t, cfg), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	return n, p.Host(), factory
}
