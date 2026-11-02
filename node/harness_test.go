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

// prebuilt is the artifact seam of a cloud that has built everything, explicit rather
// than a makeNode default since several tests are about its absence. Every built
// resource needs it: a `source.component` is a configuration id, not an artifact.
func prebuilt() Option {
	return WithArtifacts(artifactsFunc(func(_ context.Context, _, _, _, resource string) (string, error) {
		return "bafy" + resource, nil
	}))
}

// makeNode is a node over fresh resources, not started.
func makeNode(t *testing.T, cfg Config, extra ...Option) (*Node, host.Host, *memFactory) {
	t.Helper()

	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })

	h := newHost(t, mn)
	factory := &memFactory{}

	// A DNS node gets an ephemeral loopback port: :53 needs privilege and would answer for
	// the developer's resolver. An empty Services list means All, so ask as withDefaults
	// does.
	if (len(cfg.Services) == 0 || cfg.runs(DNS)) && cfg.DNSListen == "" {
		cfg.DNSListen = "127.0.0.1:0"
	}

	opts := append([]Option{
		WithHost(h),
		WithPubSub(newPubSub(t, h)),
		WithDatabases(factory),
		WithRuntimes(&tier{name: "oci"}),
		// A raft cluster of one, in memory. What this composition is asked to
		// prove is the wiring; that raft replicates is pkg/raft's own test.
		WithRaft(func(string) (raft.Cluster, error) { return raft.NewMockCluster(), nil }),
		// A secrets node keeps its shares on this machine's disk.
		WithRoot(t.TempDir()),
	}, extra...)

	n, err := New(cfg, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { n.Close() })
	return n, h, factory
}
