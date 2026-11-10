package hosted

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
	primitivesService "github.com/taubyte/tau/services/primitives"
	registerService "github.com/taubyte/tau/services/register"
	secretsService "github.com/taubyte/tau/services/secrets"
	statsService "github.com/taubyte/tau/services/stats"
	"github.com/taubyte/tau/services/worker"
	"github.com/taubyte/tau/services/worker/stream"
)

// A worker is tested on a test host beside the real services it reaches — the
// register, stats, the vault and the primitives service — with fake resources: an
// in-memory kvdb, raft groups of one and a recording tier.

// The services a test's node runs, as a node's config names them.
const (
	Register   = "register"
	Stats      = "stats"
	Secrets    = "secrets"
	Primitives = "primitives"
	Worker     = "worker"
)

// Config is what a test's node runs and how.
type Config struct {
	Services []string
	// Root is the node's own directory; NoRoot is a node with none.
	Root      string
	NoRoot    bool
	Worker    worker.Config
	Container containers.Declared
	Sections  map[string]any
}

func (c Config) runs(name string) bool {
	for _, s := range c.Services {
		if s == name {
			return true
		}
	}
	return false
}

// Option is what whoever composes a test's node hands it.
type Option func(*options)

type options struct {
	runtimes []invoke.Runtime
	built    bool
	factory  opensDatabases
	groups   raft.Groups
	now      func() time.Time
}

// WithRuntimes are the tiers the node was given.
func WithRuntimes(rts ...invoke.Runtime) Option {
	return func(o *options) { o.runtimes = append(o.runtimes, rts...) }
}

// withoutTiers is a node handed no tier at all.
func withoutTiers() Option { return func(o *options) { o.runtimes = nil } }

// prebuilt is the artifact lookup of a cloud that has built everything.
func prebuilt() Option {
	return func(o *options) { o.built = true }
}

// withKVDB opens the node's databases from a double a test can see into.
func withKVDB(f opensDatabases) Option { return func(o *options) { o.factory = f } }

// withRaft is the node's consensus groups.
func withRaft(g raft.Groups) Option { return func(o *options) { o.groups = g } }

// WithClock is the node's clock.
func WithClock(now func() time.Time) Option { return func(o *options) { o.now = now } }

// node is a worker started on a test host beside the services it reaches. Its
// Service is unstarted when the config does not run a worker.
type node struct {
	*Service
	host       *testHost
	register   registerService.Service
	stats      statsService.Service
	primitives primitivesService.Service
	secrets    secretsService.Service
}

func (n *node) Register() registerService.Service     { return n.register }
func (n *node) Stats() statsService.Service           { return n.stats }
func (n *node) Primitives() primitivesService.Service { return n.primitives }
func (n *node) Secrets() secretsService.Service       { return n.secrets }

// makeNode starts the services cfg names on a fresh peer, in a node's order, and the
// worker last; each closes with the test. Its databases are a double the test can see
// into, and its raft groups are in memory.
func makeNode(t *testing.T, mn *peer.Mocknet, cfg Config, opts ...Option) (*node, host.Host, *memFactory) {
	t.Helper()
	n, factory, err := startNode(t, mn, cfg, opts...)
	require.NoError(t, err)
	return n, n.host.Peer().Host(), factory
}

// startNode is makeNode, saying why a service would not start.
func startNode(t *testing.T, mn *peer.Mocknet, cfg Config, opts ...Option) (*node, *memFactory, error) {
	t.Helper()
	factory := &memFactory{}
	o := options{factory: factory, groups: mockGroups{}, runtimes: []invoke.Runtime{&tier{name: "oci"}}}
	for _, apply := range opts {
		apply(&o)
	}
	if cfg.Root == "" && !cfg.NoRoot {
		cfg.Root = t.TempDir()
	}
	p := mn.Mock(t.Context())
	t.Cleanup(p.Close)
	h := &testHost{
		Node: p, root: cfg.Root, now: o.now, cfg: cfg,
		Served: service.NewServed(p.ID(), nil, rpc.Retry{First: 5 * time.Millisecond, Max: 20 * time.Millisecond, For: 100 * time.Millisecond}),
	}
	h.provide("", databases{o.factory})
	if o.groups != nil {
		h.provide("", o.groups)
	}
	if p.Messaging() != nil {
		b := stream.NewBus(p.Messaging(), h.Cloud())
		t.Cleanup(func() { b.Close() })
		h.provide("", nodeBus{b})
	}

	n := &node{Service: New(), host: h}
	n.given = o.runtimes
	if o.built {
		n.lookup = everythingBuilt{}
	}
	if cfg.runs(Stats) {
		n.stats = statsService.New()
	}
	if cfg.runs(Register) {
		n.register = registerService.New()
	}
	if cfg.runs(Secrets) {
		n.secrets = secretsService.New()
	}
	if cfg.runs(Primitives) {
		n.primitives = primitivesService.New()
	}
	for _, s := range []named{{Stats, n.stats}, {Register, n.register}, {Secrets, n.secrets}, {Primitives, n.primitives}} {
		if s.service == nil {
			continue
		}
		t.Cleanup(func() { s.service.Close() })
		if err := s.service.Start(t.Context(), h.as(s.name)); err != nil {
			return n, factory, fmt.Errorf("starting %s: %w", s.name, err)
		}
		h.provide(s.name, s.service)
	}
	if cfg.runs(Worker) {
		t.Cleanup(func() { n.Service.Close() })
		if err := n.Service.Start(t.Context(), h.as(Worker)); err != nil {
			return n, factory, err
		}
		h.provide(Worker, n.Service)
	}
	return n, factory, nil
}

// named is one service a test's node runs, under its name.
type named struct {
	name    string
	service service.Service
}

// testHost is a node as the services on it see it.
type testHost struct {
	peer.Node
	*service.Served
	root string
	now  func() time.Time
	cfg  Config

	mu      sync.Mutex
	running []service.Running
}

func (h *testHost) provide(name string, s any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = append(h.running, service.Running{Name: name, Service: s})
}

func (h *testHost) as(name string) service.Host { return hostAs{h, name} }

func (h *testHost) Peer() peer.Node { return h.Node }
func (h *testHost) Running() []service.Running {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]service.Running(nil), h.running...)
}
func (h *testHost) Root() string   { return h.root }
func (h *testHost) Cloud() string  { return "worker-test" }
func (h *testHost) Region() string { return "" }
func (h *testHost) Now() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// hostAs is the host as the service named sees it: its own section, beside the
// machine's container tier.
type hostAs struct {
	*testHost
	name string
}

func (h hostAs) Config(into any) error {
	if err := roundTrip(map[string]any{"container": h.cfg.Container}, into); err != nil {
		return err
	}
	if section, ok := h.cfg.Sections[h.name]; ok {
		return roundTrip(section, into)
	}
	if h.name == Worker {
		return roundTrip(h.cfg.Worker, into)
	}
	return nil
}

func roundTrip(from, into any) error {
	raw, err := yaml.Marshal(from)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}

// nodeBus is the node's bus.
type nodeBus struct{ b *stream.Bus }

func (b nodeBus) Bus() *stream.Bus { return b.b }

// databases is the node's databases over a double.
type databases struct{ f opensDatabases }

func (d databases) Database(name string) (kvdb.Instance, error) { return d.f.New(nil, name, 0) }
func (databases) Holding(context.Context, string) (bool, error) { return false, nil }
func (databases) Paths(context.Context, string, int) ([]string, error) {
	return nil, nil
}

// opensDatabases is what a test's database double does.
type opensDatabases interface {
	New(logger log.StandardLogger, path string, rebroadcastIntervalSec int) (kvdb.Instance, error)
}

// mockGroups opens a fresh group of one, in memory, for every namespace.
type mockGroups struct{}

func (mockGroups) Group(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }

// oneGroup answers every namespace with the same group.
type oneGroup struct{ raft.Cluster }

func (g oneGroup) Group(string) (raft.Cluster, error) { return g.Cluster, nil }

// memFactory is a kvdb.Factory over maps: what matters here is that one is asked for
// and used.
type memFactory struct {
	mu     sync.Mutex
	opened []string
	closed int
	err    error

	// byPath is one store per NAME, which is what a real factory gives: a database is
	// named, and opening it twice is opening the same rows.
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

	// listErr is a store that answers reads but cannot scan.
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

// List is a prefix scan answering whole keys, in the shape a real kvdb answers with.
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
// it, a prefix keeping its trailing separator.
func datastoreKey(key string) string {
	cleaned := path.Clean("/" + key)
	if cleaned == "/" {
		return "/"
	}
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

var errTierStartsNothing = errors.New("tier: nothing is started in this test")

// tier records what it was asked to start: what a worker's composition proves is that
// the pool was built and handed over, not what a tier does with a spec.
type tier struct {
	name string

	mu      sync.Mutex
	started []invoke.Spec
}

func (t *tier) Name() string { return t.name }

func (t *tier) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return nil, errTierStartsNothing
}

// everythingBuilt answers that every resource was built, to "bafy" and its id.
type everythingBuilt struct{}

func (everythingBuilt) Artifact(_ context.Context, _, _, _, resource string) (string, error) {
	return "bafy" + resource, nil
}

// overDouble is a worker built of nothing but databases opened from f.
func overDouble(t *testing.T, f opensDatabases) *node {
	t.Helper()
	return &node{Service: &Service{dbs: databases{f}}}
}

// sees waits until a node's stats view shows name: what placement chooses from.
func sees(t *testing.T, n *node, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(n.Stats().Route(name, time.Minute)) > 0
	}, 30*time.Second, 100*time.Millisecond, "%s never appeared in the view", name)
}

// tierNames is what a worker's pool can run.
func tierNames(n *node) []string {
	var out []string
	for _, rt := range n.Tiers() {
		out = append(out, rt.Name())
	}
	return out
}
