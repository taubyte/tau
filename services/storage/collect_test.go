package storage

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/services/cm/settings"
)

// putOn writes content as the next version of name through node 0 and returns its
// root.
func putOn(t *testing.T, c *cluster, at Address, name, content string, o Object) cid.Cid {
	t.Helper()
	v, err := c.client(t, 0, 0).Put(t.Context(), at, name, o, strings.NewReader(content))
	require.NoError(t, err)
	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	return root
}

func collectAll(t *testing.T, c *cluster) {
	t.Helper()
	for i, n := range c.nodes {
		require.NoError(t, n.collect(t.Context()), "node %d", i)
	}
}

func sweepAll(t *testing.T, c *cluster) {
	t.Helper()
	for i, n := range c.nodes {
		_, err := n.sweeper.Sweep(t.Context())
		require.NoError(t, err, "node %d", i)
	}
}

// referencedOn is how many nodes hold a reference to root.
func referencedOn(t *testing.T, c *cluster, root cid.Cid) int {
	t.Helper()
	n := 0
	for _, node := range c.nodes {
		refs, err := node.local.ReferencesTo(t.Context(), root)
		require.NoError(t, err)
		if len(refs) > 0 {
			n++
		}
	}
	return n
}

// heldOn is how many nodes hold root's bytes.
func heldOn(c *cluster, root cid.Cid) int {
	n := 0
	for _, id := range c.ids {
		if c.swarm.holds(id, root) {
			n++
		}
	}
	return n
}

// shardOn is node i's copy of the instance's index.
func shardOn(t *testing.T, c *cluster, i int, at Address) *orderedMem {
	t.Helper()
	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	c.shards[i].mu.Lock()
	defer c.shards[i].mu.Unlock()
	store, ok := c.shards[i].open[shard]
	require.True(t, ok, "node %d does not hold %s's shard", i, at)
	return store
}

// A VERSION A LATER ONE SUPERSEDED IS COLLECTED ONCE THE GRACE WINDOW HAS PASSED
// SINCE THIS NODE FOUND IT SO, and its bytes swept; the current version is untouched.
func TestAVersionSupersededPastTheGraceIsCollected(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	old := putOn(t, c, at, "page", "the first page", Object{})
	current := putOn(t, c, at, "page", "the second page", Object{})

	collectAll(t, c)
	sweepAll(t, c)
	require.Equal(t, 3, referencedOn(t, c, old), "a version was collected within the grace window")
	require.Equal(t, 3, heldOn(c, old))

	clk.advance(c.nodes[0].cfg.GraceGC)
	collectAll(t, c)
	require.Zero(t, referencedOn(t, c, old), "a version superseded past the grace window was kept")
	sweepAll(t, c)
	require.Zero(t, heldOn(c, old), "the bytes of a collected version were kept")
	require.Equal(t, 3, referencedOn(t, c, current))
	require.Equal(t, 3, heldOn(c, current))
	got, err := readOn(t, c, 1, current)
	require.NoError(t, err)
	require.Equal(t, "the second page", got)
}

// A DELETED OBJECT IS COLLECTED: its tombstone supersedes it like any later version.
func TestADeletedObjectIsCollected(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	root := putOn(t, c, at, "page", "the page", Object{})
	_, err := c.client(t, 0, 0).Delete(t.Context(), at, "page")
	require.NoError(t, err)

	collectAll(t, c)
	clk.advance(c.nodes[0].cfg.GraceGC)
	collectAll(t, c)
	sweepAll(t, c)
	require.Zero(t, referencedOn(t, c, root))
	require.Zero(t, heldOn(c, root))
}

// A BUCKET THAT KEEPS VERSIONS KEEPS WHAT IT REPLACED, however long ago.
func TestAVersionedBucketKeepsWhatItReplaced(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	old := putOn(t, c, at, "page", "the first page", Object{Versioned: true})
	putOn(t, c, at, "page", "the second page", Object{Versioned: true})
	_, err := c.client(t, 0, 0).Delete(t.Context(), at, "page")
	require.NoError(t, err)

	collectAll(t, c)
	clk.advance(c.nodes[0].cfg.GraceGC + reask)
	collectAll(t, c)
	sweepAll(t, c)
	require.Equal(t, 3, referencedOn(t, c, old), "a kept version was collected")
	require.Equal(t, 3, heldOn(c, old))
}

// A VERSION PAST ITS BUCKET'S EXPIRY IS GONE AND COLLECTED, current and kept though it
// is: S5, any version older than the limit goes.
func TestAnExpiredVersionIsGoneAndCollected(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	root := putOn(t, c, at, "page", "the page", Object{Versioned: true, Expire: time.Hour})
	cl := c.client(t, 1, 1)
	_, err := cl.Current(t.Context(), at, "page")
	require.NoError(t, err)

	clk.advance(time.Hour)
	_, err = cl.Current(t.Context(), at, "page")
	require.ErrorIs(t, err, ErrNotFound, "a version past its expiry was current")
	listed, err := cl.List(t.Context(), at, "", "", 0)
	require.NoError(t, err)
	require.Empty(t, listed)

	collectAll(t, c)
	clk.advance(c.nodes[0].cfg.GraceGC)
	collectAll(t, c)
	sweepAll(t, c)
	require.Zero(t, referencedOn(t, c, root), "an expired version was kept")
	require.Zero(t, heldOn(c, root))
}

// FEWER INDEX HOLDERS THAN A WRITE IS ACKNOWLEDGED ON SAYING A NEWER ROW EXISTS SETTLE
// NOTHING: a row on one holder may be a write that failed, and go with it.
func TestAVersionIsSupersededOnlyOnABarrierOfHolders(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	old := putOn(t, c, at, "page", "the first page", Object{})
	putOn(t, c, at, "page", "the second page", Object{})

	// The newer row stays on node 0's copy alone.
	newer := newestRow(t, shardOn(t, c, 0, at), at, "page")
	for _, i := range []int{1, 2} {
		require.NoError(t, shardOn(t, c, i, at).Delete(t.Context(), ds.RawKey(newer.key)))
	}
	collectAll(t, c)
	clk.advance(c.nodes[0].cfg.GraceGC)
	collectAll(t, c)
	require.Equal(t, 3, referencedOn(t, c, old), "one holder's newer row superseded a version")

	// A second holder merges it: now as many as a write is acknowledged on hold it.
	require.NoError(t, shardOn(t, c, 1, at).Put(t.Context(), ds.RawKey(newer.key), newer.value))
	clk.advance(reask)
	collectAll(t, c)
	clk.advance(c.nodes[0].cfg.GraceGC)
	collectAll(t, c)
	require.Zero(t, referencedOn(t, c, old))
}

func newestRow(t *testing.T, store *orderedMem, at Address, name string) row {
	t.Helper()
	prefix, err := at.objectPrefix(name)
	require.NoError(t, err)
	r, ok, err := index{store: store}.first(t.Context(), prefix)
	require.NoError(t, err)
	require.True(t, ok)
	return r
}

// abandoned is a version every node keeps the bytes of and whose row no holder has: a
// write that failed after its bytes were held.
func abandoned(t *testing.T, c *cluster, at Address, content string) (cid.Cid, uint64) {
	t.Helper()
	n := c.nodes[0]
	root, _, blocks, err := n.bytes.Add(t.Context(), strings.NewReader(content))
	require.NoError(t, err)
	stamp, err := n.stamp(t.Context())
	require.NoError(t, err)
	require.NoError(t, n.keep(t.Context(), root, blocks, at, "ghost", stamp))
	for _, other := range c.nodes[1:] {
		require.NoError(t, other.hold(t.Context(), root, c.ids[0], at, "ghost", stamp))
	}
	return root, stamp
}

// A WRITE THAT NEVER BECAME A VERSION IS COLLECTED once every index holder has said,
// in two passes the grace window apart, that it holds no row of it.
func TestAnAbandonedWriteIsCollectedOnTwoAbsencesTheGraceApart(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	root, _ := abandoned(t, c, at, "a write that failed")
	grace := c.nodes[0].cfg.GraceGC

	collectAll(t, c)
	clk.advance(grace - time.Minute)
	collectAll(t, c)
	require.Equal(t, 3, referencedOn(t, c, root), "a write absent for less than the grace window was collected")

	// The pass that drops it asks again: absent then too.
	clk.advance(reask)
	collectAll(t, c)
	require.Zero(t, referencedOn(t, c, root))
	sweepAll(t, c)
	require.Zero(t, heldOn(c, root))
}

// A ROW FOUND SINCE IS PRESENCE, and forgets the absence: a holder that had not merged
// the row yet answered as if there were none.
func TestARowFoundAfterAnAbsenceKeepsTheVersion(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	root, stamp := abandoned(t, c, at, "a row on its way")
	n := c.nodes[0]
	require.NoError(t, n.collect(t.Context()))
	_, absent, err := n.local.Observed(t.Context(), at, "ghost", stamp)
	require.NoError(t, err)
	require.False(t, absent.IsZero())

	rec := VersionRecord{RootCID: root.String(), Writer: n.self}
	require.NoError(t, c.nodes[2].appendHere(t.Context(), at, "ghost", stamp, rec))
	clk.advance(n.cfg.GraceGC)
	collectAll(t, c)
	require.Equal(t, 3, referencedOn(t, c, root), "a version a holder holds was collected as abandoned")
	_, absent, err = n.local.Observed(t.Context(), at, "ghost", stamp)
	require.NoError(t, err)
	require.True(t, absent.IsZero(), "a row found did not forget the absence")
}

// A HOLDER THAT DOES NOT ANSWER IS NOT AN ABSENCE: only every holder placement names
// saying it holds no row is.
func TestAHolderThatDoesNotAnswerIsNoAbsence(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	root, stamp := abandoned(t, c, at, "a write that failed")
	n := c.nodes[0]

	shard, err := shardOf(at.Bucket())
	require.NoError(t, err)
	_, _, err = c.nodes[2].shards.reconcile(t.Context(), map[uint16]struct{}{}, c.nodes[2].shards.mark())
	require.NoError(t, err)
	require.NotContains(t, c.shards[2].held(), shard)

	require.NoError(t, n.collect(t.Context()))
	clk.advance(n.cfg.GraceGC + reask)
	require.NoError(t, n.collect(t.Context()))
	refs, err := n.local.ReferencesTo(t.Context(), root)
	require.NoError(t, err)
	require.NotEmpty(t, refs)
	_, absent, err := n.local.Observed(t.Context(), at, "ghost", stamp)
	require.NoError(t, err)
	require.True(t, absent.IsZero(), "an answer that never came counted as an absence")
}

// THE INDEX HOLDER'S ANSWER: an open shard says what its copy holds of a version, and
// a shard not open says nothing rather than open an empty copy to answer from.
func TestOnlyAnOpenShardSaysWhatItHoldsOfAVersion(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 3, fixedCatalog{at})
	root := putOn(t, c, at, "page", "the page", Object{})
	v, err := c.client(t, 0, 0).Current(t.Context(), at, "page")
	require.NoError(t, err)

	st, err := c.nodes[1].answerSuperseded(t.Context(), at, "page", v.Stamp, root)
	require.NoError(t, err)
	require.Equal(t, standing{held: true}, st)

	other := unheldBucket(t, c.shards[1].held())
	_, err = c.nodes[1].answerSuperseded(t.Context(), other, "page", v.Stamp, root)
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
}

// unheldBucket is an instance whose shard is none of held.
func unheldBucket(t *testing.T, held []uint16) Address {
	t.Helper()
	for i := range 64 {
		a, err := NewAddress("elsewhere", "prod", fmt.Sprintf("bucket-%d", i), "main")
		require.NoError(t, err)
		shard, err := shardOf(a.Bucket())
		require.NoError(t, err)
		if !slices.Contains(held, shard) {
			return a
		}
	}
	require.FailNow(t, "every draw fell in a held shard")
	return Address{}
}

// writtenOutside is a root node 0 wrote that placement over all four nodes does not
// put on node 0.
func writtenOutside(t *testing.T, c *cluster, at Address) (cid.Cid, []string) {
	t.Helper()
	for i := range 64 {
		content := fmt.Sprintf("the artifact, build %d", i)
		root := putOn(t, c, at, fmt.Sprintf("build-%d", i), content, Object{})
		holders, err := blockHolders(root.String(), c.nodes[0].placement(), settings.Defaults().StorageReplicas)
		require.NoError(t, err)
		if !slices.Contains(holders, c.ids[0].String()) {
			for _, h := range holders {
				require.Eventually(t, func() bool { return c.swarm.holdsDurably(peerOf(t, h), root) }, 10*time.Second, 10*time.Millisecond)
			}
			return root, holders
		}
	}
	require.FailNow(t, "every draw placed node 0 among its own holders")
	return cid.Undef, nil
}

// A WRITER PLACEMENT DOES NOT NAME KEEPS ITS COPY until every holder proved the root
// and took its references, then drops it: the holders keep the bytes, and the writer's
// sweep frees its own.
func TestAWritersCopyPlacementDoesNotNameIsDroppedOnceItsHoldersProveIt(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	root, holders := writtenOutside(t, c, at)
	writer := c.nodes[0]

	refs, err := writer.local.ReferencesTo(t.Context(), root)
	require.NoError(t, err)
	require.NotEmpty(t, refs, "a writer placement does not name kept no record of the copy it holds")

	require.NoError(t, writer.repair(t.Context()))
	refs, err = writer.local.ReferencesTo(t.Context(), root)
	require.NoError(t, err)
	require.Empty(t, refs, "a copy every holder proved and took was kept")
	_, err = writer.sweeper.Sweep(t.Context())
	require.NoError(t, err)
	require.False(t, c.swarm.holds(c.ids[0], root))
	for _, h := range holders {
		require.True(t, c.swarm.holds(peerOf(t, h), root), "%s is a holder and lost its copy", h)
	}
	got, err := readOn(t, c, 0, root)
	require.NoError(t, err)
	require.Contains(t, got, "the artifact")
}

// A COPY ONE HOLDER CANNOT PROVE IS KEPT: the misplaced copy is dropped only once every
// holder placement names proved it, and a holder given the root again proved nothing
// in the pass that gave it.
func TestAMisplacedCopyIsKeptWhileAHolderCannotProveIt(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	root, holders := writtenOutside(t, c, at)
	writer := c.nodes[0]
	rotted := peerOf(t, holders[0])
	c.swarm.mu.Lock()
	c.swarm.stores[rotted][root.KeyString()] = []byte("the artifacT")
	c.swarm.mu.Unlock()

	require.NoError(t, writer.repair(t.Context()))
	refs, err := writer.local.ReferencesTo(t.Context(), root)
	require.NoError(t, err)
	require.NotEmpty(t, refs, "a copy was dropped with a holder that could not prove it")

	require.NoError(t, writer.repair(t.Context()))
	refs, err = writer.local.ReferencesTo(t.Context(), root)
	require.NoError(t, err)
	require.Empty(t, refs, "every holder proved it the pass after, and the copy was kept")
}

// A NODE PLACEMENT DOES NOT NAME THAT LOST ITS COPY gives the holders its references,
// all it has to give, and drops them.
func TestABareMisplacedNodeHandsOverItsReferences(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	root, holders := writtenOutside(t, c, at)
	writer := c.nodes[0]
	writer.bytes.(*nodeBytes).lose(root)

	require.NoError(t, writer.repair(t.Context()))
	refs, err := writer.local.ReferencesTo(t.Context(), root)
	require.NoError(t, err)
	require.Empty(t, refs)
	for _, h := range holders {
		refs, err := c.node(h).local.ReferencesTo(t.Context(), root)
		require.NoError(t, err)
		require.NotEmpty(t, refs, "%s holds the bytes with no record of what for", h)
	}
}

// A SWEEP RUNS WHEN A REFERENCE WAS DROPPED, OR ONCE resweep HAS PASSED: a pass that
// dropped nothing walks nothing.
func TestASweepIsOwedByADropOrResweep(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	n := c.nodes[0]

	stray := testCID(t, "stray")
	n.bytes.(*nodeBytes).put(stray, []byte("stray"))
	require.NoError(t, n.local.Imported(t.Context(), stray))
	require.NoError(t, n.sweepIfDue(t.Context()))
	require.True(t, c.swarm.holds(c.ids[0], stray), "a sweep ran with nothing owed")

	clk.advance(resweep)
	require.NoError(t, n.sweepIfDue(t.Context()))
	require.False(t, c.swarm.holds(c.ids[0], stray), "no sweep ran once resweep passed")

	n.owed.Store(true)
	require.NoError(t, n.sweepIfDue(t.Context()))
	require.False(t, n.owed.Load())
}

// A COLLECTION NEEDS THE REGION'S SETTINGS: without them it knows no holders to ask.
func TestCollectionRefusesWithoutTheRegionsSettings(t *testing.T) {
	mn := mocknet.New()
	t.Cleanup(func() { mn.Close() })
	h, err := mn.GenPeer()
	require.NoError(t, err)
	s, err := build(h, Config{}, withBytes(&nodeBytes{memBlocks: newMemBlocks(), swarm: newSwarm(), self: h.ID()}),
		withLocal(newOrderedMem()), withShards(&memShards{}), withCatalog(fixedCatalog{}))
	require.NoError(t, err)
	require.ErrorIs(t, s.collect(t.Context()), ErrNoSettings)
}

// THE ASKING IS BUDGETED AND SPACED: a version found live is not asked about again
// before reask, and the ones never asked go first.
func TestAVersionFoundLiveIsAskedAgainOnlyAfterReask(t *testing.T) {
	at := assetsOf(t, "site_code")
	clk := newFixedClock(1_700_000_000_000)
	c := newCluster(t, 3, fixedCatalog{at}, withClock(clk.now))
	putOn(t, c, at, "page", "the page", Object{})
	n := c.nodes[1]

	asked := func() []time.Time {
		n.collectMu.Lock()
		defer n.collectMu.Unlock()
		var out []time.Time
		for _, at := range n.asked {
			out = append(out, at)
		}
		return out
	}
	require.NoError(t, n.collect(t.Context()))
	first := asked()
	require.Len(t, first, 1)
	clk.advance(reask - time.Second)
	require.NoError(t, n.collect(t.Context()))
	require.Equal(t, first, asked(), "a version found live was asked about again before reask")
	clk.advance(time.Second)
	require.NoError(t, n.collect(t.Context()))
	require.Equal(t, []time.Time{clk.now()}, asked())
}
