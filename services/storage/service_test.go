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
	cmmock "github.com/taubyte/tau/services/cm/mock"
	"github.com/taubyte/tau/services/cm/settings"
)

// swarm is every node's blockstore, so a fetch takes from the peer it names, and a
// node marked down answers nobody. A block is durable once its node synced since
// storing it; a node marked failing cannot sync.
type swarm struct {
	mu      sync.Mutex
	stores  map[libp2ppeer.ID]map[string][]byte
	durable map[libp2ppeer.ID]map[string]bool
	down    map[libp2ppeer.ID]bool
	failing map[libp2ppeer.ID]bool
	// stalls holds a node's fetches until its channel closes.
	stalls map[libp2ppeer.ID]chan struct{}
}

func newSwarm() *swarm {
	return &swarm{
		stores:  map[libp2ppeer.ID]map[string][]byte{},
		durable: map[libp2ppeer.ID]map[string]bool{},
		down:    map[libp2ppeer.ID]bool{},
		failing: map[libp2ppeer.ID]bool{},
		stalls:  map[libp2ppeer.ID]chan struct{}{},
	}
}

func (s *swarm) holdsDurably(at libp2ppeer.ID, c cid.Cid) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.durable[at][c.KeyString()]
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

func (b *nodeBytes) Fetch(ctx context.Context, root cid.Cid, from libp2ppeer.ID) ([]cid.Cid, error) {
	b.swarm.mu.Lock()
	stall := b.swarm.stalls[b.self]
	b.swarm.mu.Unlock()
	if stall != nil {
		select {
		case <-stall:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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

func (b *nodeBytes) Block(_ context.Context, c cid.Cid) ([]byte, error) {
	b.swarm.mu.Lock()
	defer b.swarm.mu.Unlock()
	data, ok := b.swarm.stores[b.self][c.KeyString()]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotHeld, c)
	}
	return data, nil
}

// lose drops c from this node, as a disk that failed does.
func (b *nodeBytes) lose(c cid.Cid) {
	b.swarm.mu.Lock()
	delete(b.swarm.stores[b.self], c.KeyString())
	delete(b.swarm.durable[b.self], c.KeyString())
	b.swarm.mu.Unlock()
	b.mu.Lock()
	delete(b.present, c.KeyString())
	b.mu.Unlock()
}

// Delete frees c on this node, from the swarm as from its own blocks.
func (b *nodeBytes) Delete(ctx context.Context, c cid.Cid) error {
	if err := b.memBlocks.Delete(ctx, c); err != nil {
		return err
	}
	b.swarm.mu.Lock()
	defer b.swarm.mu.Unlock()
	delete(b.swarm.stores[b.self], c.KeyString())
	delete(b.swarm.durable[b.self], c.KeyString())
	return nil
}

func (b *nodeBytes) Sync(context.Context) error {
	b.swarm.mu.Lock()
	defer b.swarm.mu.Unlock()
	if b.swarm.failing[b.self] {
		return errors.New("the disk refused the sync")
	}
	if b.swarm.durable[b.self] == nil {
		b.swarm.durable[b.self] = map[string]bool{}
	}
	for k := range b.swarm.stores[b.self] {
		b.swarm.durable[b.self][k] = true
	}
	return nil
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
	nodes  []*server
	shards []*memShards
	swarm  *swarm
	ids    []libp2ppeer.ID
	// caller runs no storage: a build backend asking.
	caller host.Host
	// cm is the region's settings every node reads.
	cm *cmmock.CM
}

func newCluster(t *testing.T, n int, catalog Catalog, opts ...option) *cluster {
	t.Helper()
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	c := &cluster{swarm: newSwarm(), cm: cmmock.New(settings.Defaults())}
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
		s, err := build(h, Config{}, append([]option{
			withBytes(&nodeBytes{memBlocks: newMemBlocks(), swarm: c.swarm, self: h.ID()}),
			withLocal(newOrderedMem()),
			withShards(shards),
			withCatalog(catalog),
			withMembers(members),
			withSweep(time.Hour),
		}, opts...)...)
		require.NoError(t, err)
		s.region = c.cm
		require.NoError(t, s.serve(t.Context()))
		t.Cleanup(func() { s.Close() })
		c.nodes = append(c.nodes, s)
		c.shards = append(c.shards, shards)
	}
	return c
}

// node is the cluster's member with this id.
func (c *cluster) node(id string) *server {
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

	v, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{ContentType: "application/gzip"}, strings.NewReader("the artifact"))
	require.NoError(t, err)
	require.Equal(t, uint64(len("the artifact")), v.Record.Size)
	require.Equal(t, "application/gzip", v.Record.ContentType)

	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	holders, err := blockHolders(root.String(), c.nodes[0].placement(), settings.Defaults().StorageReplicas)
	require.NoError(t, err)
	require.Len(t, holders, settings.Defaults().StorageReplicas)

	// AS MANY AS THE CLOUD PROMISES HOLD IT DURABLY WHEN THE PUT RETURNS, and the
	// rest once their asks land.
	durable := 0
	for _, h := range holders {
		if c.swarm.holdsDurably(peerOf(t, h), root) {
			durable++
		}
	}
	require.GreaterOrEqual(t, durable, settings.Defaults().StorageBarrier, "the put returned before the cloud's promise was kept")
	for _, h := range holders {
		require.Eventually(t, func() bool {
			refs, err := c.node(h).local.ReferencesTo(t.Context(), root)
			return err == nil && len(refs) == 1 && c.swarm.holdsDurably(peerOf(t, h), root)
		}, 10*time.Second, 10*time.Millisecond, "%s is a holder and has no durable copy, or no record of what for", h)
		require.NotEmpty(t, c.node(h).local.store.(*orderedMem).synced, "%s acknowledged rows a crash can take", h)
	}

	for i := range c.nodes {
		require.Eventually(t, func() bool {
			got, err := c.client(t, i, i).Current(t.Context(), at, "build-1")
			return err == nil && got.Record.RootCID == v.Record.RootCID && got.Stamp == v.Stamp
		}, 10*time.Second, 10*time.Millisecond, "node %d never read the version written", i)
	}
}

func TestAPutTooFewNodesTookIsNoVersion(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})

	// Two of three cannot fetch from anyone: the writer alone holds it, under K.
	for _, id := range c.ids[1:] {
		c.swarm.down[id] = true
	}
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("lost"))
	require.ErrorIs(t, err, ErrUnderHeld)

	for _, id := range c.ids[1:] {
		c.swarm.down[id] = false
	}
	_, err = c.client(t, 1, 1).Current(t.Context(), at, "build-1")
	require.ErrorIs(t, err, ErrNotFound, "an unacknowledged write left a version")
}

// A HOLDER THAT STALLS HOLDS UP NO WRITE: a put returns once as many holders as the
// cloud promises have it, and the holder still fetching goes on and takes it.
func TestAHolderThatStallsHoldsUpNoWrite(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	stall := make(chan struct{})
	c.swarm.mu.Lock()
	c.swarm.stalls[c.ids[1]] = stall
	c.swarm.mu.Unlock()
	released := false
	release := func() {
		if !released {
			released = true
			close(stall)
		}
	}
	t.Cleanup(release)

	done := make(chan error, 1)
	var v Version
	go func() {
		var err error
		v, err = c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("one holder stalls"))
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("a put waited on a holder that stalled")
	}

	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	holders, err := blockHolders(root.String(), c.nodes[0].placement(), settings.Defaults().StorageReplicas)
	require.NoError(t, err)
	release()
	if slices.Contains(holders, c.ids[1].String()) {
		require.Eventually(t, func() bool { return c.swarm.holds(c.ids[1], root) }, 10*time.Second, 20*time.Millisecond,
			"the holder that stalled never took the write once it could")
	}
}

// A NODE THAT CANNOT MAKE ITS COPY DURABLE DOES NOT COUNT IT: the writer promises
// copies, and nothing brings back one a crash takes.
func TestACopyThatCannotBeMadeDurableIsNotHeld(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	for _, id := range c.ids[1:] {
		c.swarm.failing[id] = true
	}
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("volatile"))
	require.ErrorIs(t, err, ErrUnderHeld)
}

func TestADeleteIsATombstoneAcknowledgedLikeAPut(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	cl := c.client(t, 0, 1)

	put, err := cl.Put(t.Context(), at, "build-1", Object{}, strings.NewReader("one"))
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
	holders := indexHolders(shard, c.nodes[0].placement(), settings.Defaults().StorageReplicas)

	outside := -1
	for i, n := range c.nodes {
		if !slices.Contains(holders, n.self) {
			outside = i
		}
	}
	require.GreaterOrEqual(t, outside, 0, "five nodes and three holders leave someone out")

	_, err = c.client(t, outside, outside).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("one"))
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
	holders := indexHolders(shard, c.nodes[0].placement(), settings.Defaults().StorageReplicas)

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

	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("one"))
	require.NoError(t, err)
	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	for i, n := range c.nodes {
		require.Eventually(t, func() bool {
			seen, err := n.local.Seen(t.Context())
			return err == nil && slices.Contains(seen, at)
		}, 10*time.Second, 10*time.Millisecond, "node %d was never written its row", i)
		require.NoError(t, n.reconcile(t.Context()))
		require.Equal(t, []uint16{shard}, c.shards[i].held(), "node %d closed a shard it was written", i)
	}
}

func TestWhatNewRefuses(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)

	full := []option{
		withBytes(&nodeBytes{memBlocks: newMemBlocks(), swarm: newSwarm(), self: h.ID()}),
		withLocal(newOrderedMem()), withShards(&memShards{}), withCatalog(fixedCatalog{}),
	}
	type testCase struct {
		drop int
		want error
	}
	for _, tc := range []testCase{{0, ErrNoBytes}, {2, ErrNoShards}, {3, ErrNoCatalog}} {
		opts := slices.Delete(slices.Clone(full), tc.drop, tc.drop+1)
		_, err := build(h, Config{}, opts...)
		require.ErrorIs(t, err, tc.want)
	}
	_, err = build(h, Config{}, slices.Delete(slices.Clone(full), 1, 2)...)
	require.ErrorContains(t, err, "no local store")
	_, err = build(nil, Config{}, full...)
	require.Error(t, err)
	_, err = build(h, Config{}, append(full, withSweep(0))...)
	require.Error(t, err)

	s, err := build(h, Config{}, full...)
	require.NoError(t, err)
	require.NoError(t, s.serve(t.Context()))
	require.Error(t, s.serve(t.Context()), "started twice")
	require.NoError(t, s.Close())
	require.Error(t, s.serve(t.Context()), "started after close")
}

func TestAOneNodeStoreIsItsOwnBar(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 1, fixedCatalog{at})

	w, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("alone"))
	require.NoError(t, err)
	got, err := c.client(t, 0, 0).Current(t.Context(), at, "build-1")
	require.NoError(t, err)
	require.Equal(t, w.Record.RootCID, got.Record.RootCID)
}

func TestASettingChangedInCmIsReadWithNoRestart(t *testing.T) {
	ctx := t.Context()
	c := newCluster(t, 1, fixedCatalog{})

	replicas, barrier, err := c.nodes[0].promise(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.Defaults().StorageReplicas, replicas)
	require.Equal(t, settings.Defaults().StorageBarrier, barrier)

	// The client a node reads cm through keeps an answer for settings.Recheck (its
	// own tests); storage keeps none, so what cm says is what the next write uses.
	changed := settings.Defaults()
	changed.StorageReplicas, changed.StorageBarrier = 5, 3
	c.cm.Set(changed)
	replicas, barrier, err = c.nodes[0].promise(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, replicas)
	require.Equal(t, 3, barrier)
}

func TestANodeThatHasNotHeardTheRegionsSettingsWritesNothing(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 1, fixedCatalog{at})
	c.cm.Fail(errors.New("no cm in reach"))

	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("x"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "settings have not been heard")
}

func TestAPlacementThatNamesNobodyAcknowledgesNothing(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 1, fixedCatalog{at}, withMembers(func() []placement.Member { return nil }))

	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("x"))
	require.ErrorIs(t, err, ErrUnderHeld, "a write held by nobody was acknowledged")
}
