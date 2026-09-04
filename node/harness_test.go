package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

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
}

func (f *memFactory) New(_ log.StandardLogger, path string, _ int) (kvdb.KVDB, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}
	f.opened = append(f.opened, path)
	return &memKV{rows: map[string][]byte{}, factory: f}, nil
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
	v, ok := k.rows[key]
	if !ok {
		return nil, ds.ErrNotFound
	}
	return v, nil
}

// List is a prefix scan, and it returns WHOLE KEYS the way a real kvdb does —
// pkg/register trims the prefix off what comes back, so a double that answered
// relative keys would make every deployment name read as a whole pointer key.
func (k *memKV) List(_ context.Context, prefix string) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	var out []string
	for key := range k.rows {
		if strings.HasPrefix(key, prefix) {
			out = append(out, key)
		}
	}
	return out, nil
}

func (k *memKV) Put(_ context.Context, key string, v []byte) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rows[key] = v
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
