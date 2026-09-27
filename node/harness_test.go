package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"path"

	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-log/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/raft"
)

// A NODE IS TESTED WITH REAL SERVICES AND FAKE RESOURCES.
//
// What a composition root can get wrong is what is connected to what, in which
// order, and what happens when one piece is missing — none of which a mock of
// the services would show. So every service below is the real one; what is
// faked is the machine's resources: a mocknet host, a gossipsub over it, an
// in-memory kvdb and a tier that records rather than starts.

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

func (f *memFactory) New(_ log.StandardLogger, path string, _ int) (kvdb.KVDB, error) {
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
	kvdb.KVDB
	factory *memFactory

	mu   sync.Mutex
	rows map[string][]byte
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

	at := datastoreKey(prefix)
	var out []string
	for key := range k.rows {
		if strings.HasPrefix(key, at) {
			out = append(out, key)
		}
	}
	return out, nil
}

// datastoreKey is what pkg/kvdb does to every key it is handed: it wraps it in
// a datastore path, which makes the key absolute and path-CLEANS it.
//
// THE DOUBLE HAS TO DO IT TOO. Without it this fake was more permissive than
// the store it stands in for, in both directions at once — a listed key came
// back without the leading slash a real one carries, which made a prefix trim
// look correct when in production it trimmed nothing; and a key holding `..`
// was a distinct map entry here while a real store resolves it into somebody
// else's. Both of those are bugs this package shipped, and this fake is why
// their tests passed.
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

func (k *memKV) Close() {
	k.factory.mu.Lock()
	k.factory.closed++
	k.factory.mu.Unlock()
}

// tier records what it was asked to start. Starting a real container needs a
// daemon, and what a composition proves is that the pool was built and handed
// over — not what a tier does with a spec.
type tier struct {
	name string

	mu      sync.Mutex
	started []invoke.Spec
}

func (t *tier) Name() string { return t.name }

func (t *tier) Start(context.Context, invoke.Spec) (invoke.Instance, error) {
	return nil, errors.New("tier: nothing is started in this test")
}

// makeNode is a node over fresh resources, not started.
func makeNode(t *testing.T, cfg Config, extra ...Option) (*Node, host.Host, *memFactory) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	h := newHost(t, mn)
	factory := &memFactory{}

	// A DNS NODE TAKES A REAL SOCKET, and the default is :53 — which needs
	// root or CAP_NET_BIND_SERVICE, and which a test must not take even when it
	// has them: a suite that answered for the machine it runs on would be
	// answering for the developer's own resolver. An ephemeral loopback port
	// is the composer's choice here, exactly as :53 is the operator's there.
	// An empty Services list means All, which is where the DNS node comes from
	// in most of these tests — so the question is asked the way withDefaults
	// answers it rather than of the list as written.
	if (len(cfg.Services) == 0 || cfg.runs(DNS)) && cfg.DNSListen == "" {
		cfg.DNSListen = "127.0.0.1:0"
	}

	opts := append([]Option{
		WithHost(h),
		WithPubSub(newPubSub(t, h)),
		WithDatabases(factory),
		WithRuntimes(&tier{name: "container"}),
		// A raft cluster of one, in memory. What this composition is asked to
		// prove is the wiring; that raft replicates is pkg/raft's own test.
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
	}, extra...)

	n, err := New(cfg, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	return n, h, factory
}
