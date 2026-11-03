package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	mh "github.com/multiformats/go-multihash"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
)

// swarm is every node's blockstore, so a fetch takes from the peer it names, and a
// node marked down answers nobody.
type swarm struct {
	mu     sync.Mutex
	stores map[libp2ppeer.ID]map[string][]byte
	down   map[libp2ppeer.ID]bool
}

func newSwarm() *swarm {
	return &swarm{stores: map[libp2ppeer.ID]map[string][]byte{}, down: map[libp2ppeer.ID]bool{}}
}

func (s *swarm) holds(at libp2ppeer.ID, c cid.Cid) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.stores[at][c.KeyString()]
	return ok
}

// nodeBytes is one node's blockstore in the swarm. An object is one raw block.
type nodeBytes struct {
	*memBlocks
	swarm *swarm
	self  libp2ppeer.ID
}

func (b *nodeBytes) Add(_ context.Context, content io.Reader) (cid.Cid, uint64, []cid.Cid, error) {
	data, err := io.ReadAll(content)
	if err != nil {
		return cid.Undef, 0, nil, err
	}
	sum, err := mh.Sum(data, mh.SHA2_256, -1)
	if err != nil {
		return cid.Undef, 0, nil, err
	}
	c := cid.NewCidV1(cid.Raw, sum)
	b.put(c, data)
	return c, uint64(len(data)), []cid.Cid{c}, nil
}

func (b *nodeBytes) put(c cid.Cid, data []byte) {
	b.swarm.mu.Lock()
	if b.swarm.stores[b.self] == nil {
		b.swarm.stores[b.self] = map[string][]byte{}
	}
	b.swarm.stores[b.self][c.KeyString()] = data
	b.swarm.mu.Unlock()
	b.add(c)
}

func (b *nodeBytes) Fetch(_ context.Context, root cid.Cid, from libp2ppeer.ID) ([]cid.Cid, error) {
	b.swarm.mu.Lock()
	down := b.swarm.down[b.self] || b.swarm.down[from]
	data, ok := b.swarm.stores[from][root.KeyString()]
	b.swarm.mu.Unlock()
	if down {
		return nil, errors.New("unreachable")
	}
	if !ok {
		return nil, fmt.Errorf("%s does not have %s", from, root)
	}
	b.put(root, data)
	return []cid.Cid{root}, nil
}

func (b *nodeBytes) Read(_ context.Context, root cid.Cid) (io.ReadSeekCloser, error) {
	b.swarm.mu.Lock()
	defer b.swarm.mu.Unlock()
	for _, store := range b.swarm.stores {
		if data, ok := store[root.KeyString()]; ok {
			return nopSeekCloser{bytes.NewReader(data)}, nil
		}
	}
	return nil, fmt.Errorf("nobody has %s", root)
}

type nopSeekCloser struct{ *bytes.Reader }

func (nopSeekCloser) Close() error { return nil }

// memShards opens a shard as an ordered map; a down node's shards refuse writes.
type memShards struct {
	mu   sync.Mutex
	open map[uint16]*orderedMem
}

func (m *memShards) Open(_ context.Context, shard uint16) (Datastore, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.open == nil {
		m.open = map[uint16]*orderedMem{}
	}
	store := newOrderedMem()
	m.open[shard] = store
	return store, nil
}

func (m *memShards) Close(_ context.Context, shard uint16, _ Datastore) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.open, shard)
	return nil
}

func (m *memShards) held() []uint16 {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uint16
	for s := range m.open {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

type fixedCatalog []Address

func (c fixedCatalog) Buckets(context.Context) ([]Address, error) { return c, nil }

// cluster is n storage nodes on one mocknet, each the others' peer.
type cluster struct {
	nodes  []*Service
	shards []*memShards
	swarm  *swarm
	ids    []libp2ppeer.ID
	// caller runs no storage: a build backend asking.
	caller host.Host
}

func newCluster(t *testing.T, n int, catalog Catalog, opts ...Option) *cluster {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	c := &cluster{swarm: newSwarm()}
	for range n {
		h, err := mn.GenPeer()
		require.NoError(t, err)
		c.ids = append(c.ids, h.ID())
	}
	caller, err := mn.GenPeer()
	require.NoError(t, err)
	c.caller = caller
	require.NoError(t, mn.LinkAll())
	require.NoError(t, mn.ConnectAllButSelf())

	members := func() []placement.Member {
		out := make([]placement.Member, 0, n)
		for _, id := range c.ids {
			out = append(out, placement.Member{ID: id.String(), Accepts: true})
		}
		return out
	}
	for _, id := range c.ids {
		h := mn.Host(id)
		shards := &memShards{}
		s, err := New(h, Config{}, append([]Option{
			WithBytes(&nodeBytes{memBlocks: newMemBlocks(), swarm: c.swarm, self: h.ID()}),
			WithLocal(newOrderedMem()),
			WithShards(shards),
			WithCatalog(catalog),
			WithMembers(members),
			WithSweep(time.Hour),
		}, opts...)...)
		require.NoError(t, err)
		require.NoError(t, s.Start(t.Context()))
		t.Cleanup(func() { s.Close() })
		c.nodes = append(c.nodes, s)
		c.shards = append(c.shards, shards)
	}
	return c
}

// node is the cluster's member with this id.
func (c *cluster) node(id string) *Service {
	for _, n := range c.nodes {
		if n.self == id {
			return n
		}
	}
	return nil
}

// client is node from asking node to; a node never dials itself, so asking itself is
// the caller asking it.
func (c *cluster) client(t *testing.T, from, to int) *Client {
	t.Helper()
	at := c.caller
	if from != to {
		at = c.nodes[from].host
	}
	cl, err := NewClient(at, c.ids[to])
	require.NoError(t, err)
	t.Cleanup(func() { cl.Close() })
	return cl
}

func assetsOf(t *testing.T, instance string) Address {
	t.Helper()
	a, err := NewAddress("shop", "prod", AssetsBucket, instance)
	require.NoError(t, err)
	return a
}

func TestAPutIsHeldByItsHoldersAndReadFromAnyNode(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})

	v, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", "application/gzip", strings.NewReader("the artifact"))
	require.NoError(t, err)
	require.Equal(t, uint64(len("the artifact")), v.Record.Size)
	require.Equal(t, "application/gzip", v.Record.ContentType)

	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	holders, err := blockHolders(root.String(), c.nodes[0].placement(), DefaultReplicas)
	require.NoError(t, err)
	require.Len(t, holders, DefaultReplicas)
	for _, h := range holders {
		id, err := libp2ppeer.Decode(h)
		require.NoError(t, err)
		require.True(t, c.swarm.holds(id, root), "%s is a holder and has no copy", h)
		refs, err := c.node(h).local.ReferencesTo(t.Context(), root)
		require.NoError(t, err)
		require.Len(t, refs, 1, "%s holds the bytes with no record of what for", h)
	}

	for i := range c.nodes {
		got, err := c.client(t, i, i).Current(t.Context(), at, "build-1")
		require.NoError(t, err, "node %d", i)
		require.Equal(t, v.Record.RootCID, got.Record.RootCID, "node %d", i)
		require.Equal(t, v.Stamp, got.Stamp, "node %d", i)
	}
}

func TestAPutTooFewNodesTookIsNoVersion(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})

	// Two of three cannot fetch from anyone: the writer alone holds it, under K.
	for _, id := range c.ids[1:] {
		c.swarm.down[id] = true
	}
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", "", strings.NewReader("lost"))
	require.ErrorIs(t, err, ErrUnderHeld)

	for _, id := range c.ids[1:] {
		c.swarm.down[id] = false
	}
	_, err = c.client(t, 1, 1).Current(t.Context(), at, "build-1")
	require.ErrorIs(t, err, ErrNotFound, "an unacknowledged write left a version")
}

func TestADeleteIsATombstoneAcknowledgedLikeAPut(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	cl := c.client(t, 0, 1)

	put, err := cl.Put(t.Context(), at, "build-1", "", strings.NewReader("one"))
	require.NoError(t, err)
	del, err := cl.Delete(t.Context(), at, "build-1")
	require.NoError(t, err)
	require.True(t, del.Record.Tombstone)
	require.Greater(t, del.Stamp, put.Stamp)

	_, err = c.client(t, 2, 2).Current(t.Context(), at, "build-1")
	require.ErrorIs(t, err, ErrNotFound)

	vs, err := c.client(t, 2, 0).Versions(t.Context(), at, "build-1", 0)
	require.NoError(t, err)
	require.Len(t, vs, 2)
	require.True(t, vs[0].Record.Tombstone, "newest first")
	require.Equal(t, put.Record.RootCID, vs[1].Record.RootCID)

	vs, err = c.client(t, 2, 0).Versions(t.Context(), at, "build-1", 1)
	require.NoError(t, err)
	require.Len(t, vs, 1)
}

func TestANodeThatHoldsNoIndexAsksOneThatDoes(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 5, fixedCatalog{at})
	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	holders := indexHolders(shard, c.nodes[0].placement(), DefaultReplicas)

	outside := -1
	for i, n := range c.nodes {
		if !slices.Contains(holders, n.self) {
			outside = i
		}
	}
	require.GreaterOrEqual(t, outside, 0, "five nodes and three holders leave someone out")

	_, err = c.client(t, outside, outside).Put(t.Context(), at, "build-1", "", strings.NewReader("one"))
	require.NoError(t, err)
	got, err := c.client(t, outside, outside).Current(t.Context(), at, "build-1")
	require.NoError(t, err)
	require.EqualValues(t, 3, got.Record.Size)
	require.Empty(t, c.shards[outside].held(), "a node placement does not name opened the index")
}

func TestAShardIsOpenedWhereThePlacementAndTheCatalogPutIt(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	holders := indexHolders(shard, c.nodes[0].placement(), DefaultReplicas)

	for i, n := range c.nodes {
		require.NoError(t, n.reconcile(t.Context()))
		if slices.Contains(holders, n.self) {
			require.Equal(t, []uint16{shard}, c.shards[i].held(), "node %d is a holder", i)
		} else {
			require.Empty(t, c.shards[i].held(), "node %d is not", i)
		}
	}
}

func TestABucketTheCatalogHasNotHeardOfStaysOpenOnceWritten(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{})

	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", "", strings.NewReader("one"))
	require.NoError(t, err)
	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	for i, n := range c.nodes {
		require.NoError(t, n.reconcile(t.Context()))
		require.Equal(t, []uint16{shard}, c.shards[i].held(), "node %d closed a shard it was written", i)
	}
}

func TestWhatNewRefuses(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)

	full := []Option{
		WithBytes(&nodeBytes{memBlocks: newMemBlocks(), swarm: newSwarm(), self: h.ID()}),
		WithLocal(newOrderedMem()), WithShards(&memShards{}), WithCatalog(fixedCatalog{}),
	}
	type testCase struct {
		drop int
		want error
	}
	for _, tc := range []testCase{{0, ErrNoBytes}, {2, ErrNoShards}, {3, ErrNoCatalog}} {
		opts := slices.Delete(slices.Clone(full), tc.drop, tc.drop+1)
		_, err := New(h, Config{}, opts...)
		require.ErrorIs(t, err, tc.want)
	}
	_, err = New(h, Config{}, slices.Delete(slices.Clone(full), 1, 2)...)
	require.ErrorContains(t, err, "no local store")
	_, err = New(nil, Config{}, full...)
	require.Error(t, err)
	_, err = New(h, Config{}, append(full, WithSweep(0))...)
	require.Error(t, err)

	s, err := New(h, Config{}, full...)
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	require.Error(t, s.Start(t.Context()), "started twice")
	require.NoError(t, s.Close())
	require.Error(t, s.Start(t.Context()), "started after close")
}

func TestAOneNodeStoreIsItsOwnBar(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 1, fixedCatalog{at})

	w, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", "", strings.NewReader("alone"))
	require.NoError(t, err)
	got, err := c.client(t, 0, 0).Current(t.Context(), at, "build-1")
	require.NoError(t, err)
	require.Equal(t, w.Record.RootCID, got.Record.RootCID)
}
