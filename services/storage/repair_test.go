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

	"connectrpc.com/connect"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-datastore/query"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/services/cm/settings"
)

// view is the placement every node of a cluster sees, changed as a test goes.
type view struct {
	mu      sync.Mutex
	members []placement.Member
}

func (v *view) set(ids ...libp2ppeer.ID) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.members = v.members[:0]
	for _, id := range ids {
		v.members = append(v.members, placement.Member{ID: id.String(), Accepts: true})
	}
}

func (v *view) get() []placement.Member {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.members)
}

// writtenAlone is a root node 0 wrote while it was the region's only storage node,
// drawn until the holders placement names once the others join leave node 0 out.
func writtenAlone(t *testing.T, c *cluster, v *view, at Address) (root cid.Cid, content string, holders []string) {
	t.Helper()
	v.set(c.ids[0])
	all := make([]placement.Member, 0, len(c.ids))
	for _, id := range c.ids {
		all = append(all, placement.Member{ID: id.String(), Accepts: true})
	}
	for i := range 64 {
		content = fmt.Sprintf("the artifact, build %d", i)
		v, err := c.client(t, 0, 0).Put(t.Context(), at, fmt.Sprintf("build-%d", i), Object{}, strings.NewReader(content))
		require.NoError(t, err)
		root, err = cid.Decode(v.Record.RootCID)
		require.NoError(t, err)
		holders, err = blockHolders(root.String(), all, settings.Defaults().StorageReplicas)
		require.NoError(t, err)
		if !slices.Contains(holders, c.ids[0].String()) {
			return root, content, holders
		}
	}
	require.FailNow(t, "every draw placed node 0 among its own holders")
	return
}

func readOn(t *testing.T, c *cluster, i int, root cid.Cid) (string, error) {
	t.Helper()
	r, err := c.client(t, i, i).Read(t.Context(), root.String())
	if err != nil {
		return "", err
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	return string(got), err
}

func TestARootWrittenBeforeTheRegionGrewReachesTheHoldersPlacementNamesNow(t *testing.T) {
	at := assetsOf(t, "site_code")
	v := &view{}
	c := newCluster(t, 4, fixedCatalog{at}, withMembers(v.get))

	root, content, holders := writtenAlone(t, c, v, at)
	v.set(c.ids...)

	_, err := readOn(t, c, 1, root)
	require.ErrorContains(t, err, "no holder has", "the holders a reader asks do not include the writer")

	require.NoError(t, c.nodes[0].repair(t.Context()))

	for _, h := range holders {
		id, err := libp2ppeer.Decode(h)
		require.NoError(t, err)
		require.True(t, c.swarm.holds(id, root), "%s is a holder and has no copy", h)
		refs, err := c.node(h).local.ReferencesTo(t.Context(), root)
		require.NoError(t, err)
		require.NotEmpty(t, refs, "%s holds the bytes with no record of what for", h)
	}
	for i := range c.nodes {
		got, err := readOn(t, c, i, root)
		require.NoError(t, err, "node %d", i)
		require.Equal(t, content, got, "node %d", i)
	}
}

func TestAHolderThatCouldNotBeAskedIsAskedAgainAndARootProvenEverywhereIsNot(t *testing.T) {
	at := assetsOf(t, "site_code")
	v := &view{}
	c := newCluster(t, 4, fixedCatalog{at}, withMembers(v.get))

	root, _, holders := writtenAlone(t, c, v, at)
	v.set(c.ids...)

	unreachable, err := libp2ppeer.Decode(holders[0])
	require.NoError(t, err)
	c.swarm.mu.Lock()
	c.swarm.down[unreachable] = true
	c.swarm.mu.Unlock()
	require.Error(t, c.nodes[0].repair(t.Context()))
	require.False(t, c.swarm.holds(unreachable, root))

	c.swarm.mu.Lock()
	c.swarm.down[unreachable] = false
	c.swarm.mu.Unlock()
	require.NoError(t, c.nodes[0].repair(t.Context()))
	require.True(t, c.swarm.holds(unreachable, root), "a holder that was down was not asked again")

	// Every holder down: a root every holder proved is not proven again before
	// reprove.
	c.swarm.mu.Lock()
	for _, id := range c.ids {
		c.swarm.down[id] = true
	}
	c.swarm.mu.Unlock()
	require.NoError(t, c.nodes[0].repair(t.Context()))

	// A placement that changes is proven and handed over again.
	v.set(c.ids[1:]...)
	require.Error(t, c.nodes[0].repair(t.Context()))
}

// heldBy is a root an object put through node 0 is stored under, and its holders.
func heldBy(t *testing.T, c *cluster, at Address, content string) (cid.Cid, []string) {
	t.Helper()
	v, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader(content))
	require.NoError(t, err)
	root, err := cid.Decode(v.Record.RootCID)
	require.NoError(t, err)
	holders, err := blockHolders(root.String(), c.nodes[0].placement(), settings.Defaults().StorageReplicas)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(holders), 3)
	for _, h := range holders {
		require.Eventually(t, func() bool { return c.swarm.holdsDurably(peerOf(t, h), root) }, 10*time.Second, 10*time.Millisecond)
	}
	return root, holders
}

func peerOf(t *testing.T, id string) libp2ppeer.ID {
	t.Helper()
	p, err := libp2ppeer.Decode(id)
	require.NoError(t, err)
	return p
}

// A HOLDER THAT LOST ITS COPY IS FOUND AND GIVEN IT BACK, within reprove of losing
// it, by another holder that proves it: a holder whose disk failed keeps its place,
// and nothing else would ever notice the copy was gone.
func TestAHolderThatLostItsCopyIsFoundAndGivenItBack(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	root, holders := heldBy(t, c, at, "the artifact")
	prover, lost := c.node(holders[0]), holders[1]

	require.NoError(t, prover.repair(t.Context()))
	c.node(lost).bytes.(*nodeBytes).lose(root)
	require.False(t, c.swarm.holds(peerOf(t, lost), root))

	require.NoError(t, prover.repair(t.Context()))
	require.False(t, c.swarm.holds(peerOf(t, lost), root), "a root every holder proved was proven again before reprove")

	later := time.Now().Add(reprove + time.Minute)
	prover.now = func() time.Time { return later }
	require.NoError(t, prover.repair(t.Context()))
	require.True(t, c.swarm.holds(peerOf(t, lost), root), "a holder that lost its copy was never given it back")
}

// A HOLDER PLACEMENT NAMES THAT LOST ITS OWN COPY TAKES IT BACK, from another holder,
// and proves the rest against it.
func TestAHolderThatLostItsOwnCopyTakesItBack(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	root, holders := heldBy(t, c, at, "the artifact")
	lost := c.node(holders[0])
	lost.bytes.(*nodeBytes).lose(root)

	require.NoError(t, lost.repair(t.Context()))
	require.True(t, c.swarm.holds(peerOf(t, holders[0]), root), "a holder that lost its own copy did not take it back")
	p := lost.proofs[root.KeyString()]
	require.True(t, p.settled)
	require.Equal(t, len(holders)-1, p.proven)
}

// A CLAIM PROVES NOTHING: a holder whose copy rotted answers a probe, wrongly, and
// is given the root again.
func TestACopyThatRottedProvesNothingAndIsReplaced(t *testing.T) {
	at := assetsOf(t, "site_code")
	c := newCluster(t, 4, fixedCatalog{at})
	root, holders := heldBy(t, c, at, "the artifact")
	prover, rotted := c.node(holders[0]), peerOf(t, holders[1])

	c.swarm.mu.Lock()
	c.swarm.stores[rotted][root.KeyString()] = []byte("the artifacT")
	c.swarm.mu.Unlock()

	require.NoError(t, prover.repair(t.Context()))
	c.swarm.mu.Lock()
	got := string(c.swarm.stores[rotted][root.KeyString()])
	c.swarm.mu.Unlock()
	require.Equal(t, "the artifact", got, "a holder that answered a probe wrongly was counted")
	require.Equal(t, len(holders)-2, prover.proofs[root.KeyString()].proven)
}

// probeDAG is a root with two children of two leaves each, on one node's blocks.
func probeDAG(t *testing.T) (*memBlocks, cid.Cid, []cid.Cid) {
	t.Helper()
	b := newMemBlocks()
	var leaves, children []cid.Cid
	for i := range 2 {
		l1, l2 := testCID(t, fmt.Sprintf("leaf %d.1", i)), testCID(t, fmt.Sprintf("leaf %d.2", i))
		child := testCID(t, fmt.Sprintf("child %d", i))
		b.add(l1)
		b.add(l2)
		b.add(child, l1, l2)
		leaves = append(leaves, l1, l2)
		children = append(children, child)
	}
	root := testCID(t, "root")
	b.add(root, children...)
	return b, root, leaves
}

// A PROBE'S ANSWER IS THE BLOCKS' TO GIVE: two nodes holding the same blocks answer a
// nonce alike, another nonce differently, and one missing what the nonce reaches
// cannot answer at all.
func TestAProbeIsAnsweredOnlyFromTheBlocks(t *testing.T) {
	ctx := t.Context()
	a, root, leaves := probeDAG(t)
	b, _, _ := probeDAG(t)
	nonce := bytes.Repeat([]byte{7}, nonceSize)

	pa, err := possession(ctx, a, root, nonce)
	require.NoError(t, err)
	pb, err := possession(ctx, b, root, nonce)
	require.NoError(t, err)
	require.Equal(t, pa, pb)
	other, err := possession(ctx, a, root, bytes.Repeat([]byte{8}, nonceSize))
	require.NoError(t, err)
	require.NotEqual(t, pa, other)

	for _, l := range leaves {
		b.mu.Lock()
		delete(b.present, l.KeyString())
		b.mu.Unlock()
	}
	_, err = possession(ctx, b, root, nonce)
	require.ErrorIs(t, err, ErrNotHeld)
}

// A NONCE TOO SHORT TO BE FRESH, OR TOO LONG TO BE WORTH READING, IS REFUSED before
// any block is.
func TestAProbeWithANonceOutOfBoundsIsRefused(t *testing.T) {
	s := &server{bytes: &nodeBytes{memBlocks: newMemBlocks(), swarm: newSwarm()}}
	for _, n := range []int{minNonce - 1, maxNonce + 1} {
		_, err := s.answerProbe(t.Context(), testCID(t, "root"), make([]byte, n))
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a %d-byte nonce", n)
	}
	_, err := s.answerProbe(t.Context(), testCID(t, "root"), make([]byte, nonceSize))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "a root this node does not hold")
}

// THE FEWEST PROVEN GO FIRST: roots never proven, then the fewest holders, then the
// longest ago; a root settled under this placement and these references waits for
// reprove, and a pass takes at most repairBudget.
func TestUnderReplicatedRootsAreRepairedFirst(t *testing.T) {
	s := &server{proofs: map[string]proof{}}
	now := time.Now()
	r := func(label string) rooted { return rooted{root: testCID(t, label), refs: make([]Reference, 1)} }
	never, one, oneOlder, two, settled, stale := r("never"), r("one"), r("one, older"), r("two"), r("settled"), r("stale")
	s.proofs[one.root.KeyString()] = proof{view: "v", refs: 1, at: now, proven: 1}
	s.proofs[oneOlder.root.KeyString()] = proof{view: "v", refs: 1, at: now.Add(-time.Minute), proven: 1}
	s.proofs[two.root.KeyString()] = proof{view: "v", refs: 1, at: now, proven: 2}
	s.proofs[settled.root.KeyString()] = proof{view: "v", refs: 1, at: now, proven: 2, settled: true}
	s.proofs[stale.root.KeyString()] = proof{view: "v", refs: 1, at: now.Add(-reprove), proven: 2, settled: true}

	due := s.due([]rooted{two, settled, one, stale, never, oneOlder}, "v", now)
	var got []cid.Cid
	for _, d := range due {
		got = append(got, d.root)
	}
	require.Equal(t, []cid.Cid{never.root, oneOlder.root, one.root, stale.root, two.root}, got)

	require.Len(t, s.due([]rooted{settled}, "another placement", now), 1, "a placement change left a settled root unproven")
	settledMore := settled
	settledMore.refs = make([]Reference, 2)
	require.Len(t, s.due([]rooted{settledMore}, "v", now), 1, "a new reference left a settled root unproven")

	var many []rooted
	for i := range repairBudget + 5 {
		many = append(many, r(fmt.Sprintf("root %d", i)))
	}
	require.Len(t, s.due(many, "v", now), repairBudget)
}

func TestAPlacementIsNamedByWhoAcceptsAndHowManyCopies(t *testing.T) {
	a := []placement.Member{{ID: "b", Accepts: true}, {ID: "a", Accepts: true}, {ID: "c"}}
	b := []placement.Member{{ID: "a", Accepts: true}, {ID: "b", Accepts: true}}
	require.Equal(t, placementView(a, 3), placementView(b, 3), "order and a member that declines change nothing")
	require.NotEqual(t, placementView(b, 3), placementView(b, 2))
	require.NotEqual(t, placementView(b, 3), placementView(b[:1], 3))
}

// unlistable is a local store whose rows cannot be listed.
type unlistable struct{ *orderedMem }

func (unlistable) Query(context.Context, query.Query) (query.Results, error) {
	return nil, errors.New("the disk is gone")
}

func TestReferencesThatCannotBeListedAreAnError(t *testing.T) {
	at := assetsOf(t, "site_code")
	v := &view{}
	c := newCluster(t, 4, fixedCatalog{at}, withMembers(v.get))

	writtenAlone(t, c, v, at)
	v.set(c.ids...)
	local, err := NewLocal(unlistable{newOrderedMem()}, time.Now)
	require.NoError(t, err)
	c.nodes[0].local = local

	require.ErrorContains(t, c.nodes[0].repair(t.Context()), "the disk is gone")
}

// A SHARD PLACEMENT MOVES OFF A NODE STAYS OPEN THERE UNTIL ITS NEW HOLDERS HAVE IT:
// as many of them as a write is acknowledged on must have merged everything the
// node's copy holds. A node that wrote alone before the region grew holds the only
// copy, and closing it early leaves those rows served by nobody.
func TestAShardMovedOffANodeIsKeptUntilItsNewHoldersHaveIt(t *testing.T) {
	// A shard is a bucket's, so the candidates differ by bucket.
	var cands []Address
	for i := range 64 {
		a, err := NewAddress(fmt.Sprintf("shop%d", i), "prod", AssetsBucket, "site_code")
		require.NoError(t, err)
		cands = append(cands, a)
	}
	v := &view{}
	c := newCluster(t, 4, fixedCatalog(cands), withMembers(v.get))
	all := make([]placement.Member, 0, len(c.ids))
	for _, id := range c.ids {
		all = append(all, placement.Member{ID: id.String(), Accepts: true})
	}
	var at Address
	var shard uint16
	var holders []string
	for _, a := range cands {
		s, err := shardOf(a.Bucket())
		require.NoError(t, err)
		if h := indexHolders(s, all, settings.Defaults().StorageReplicas); !slices.Contains(h, c.ids[0].String()) {
			at, shard, holders = a, s, h
			break
		}
	}
	require.NotEmpty(t, holders, "every candidate's shard placed node 0 among its holders")

	v.set(c.ids[0])
	_, err := c.client(t, 0, 0).Put(t.Context(), at, "build-1", Object{}, strings.NewReader("written alone"))
	require.NoError(t, err)
	written := c.shards[0].open[shard]
	require.NotNil(t, written)

	v.set(c.ids...)
	kept := func(why string) {
		t.Helper()
		require.NoError(t, c.nodes[0].reconcile(t.Context()))
		require.Contains(t, c.shards[0].held(), shard, why)
	}
	kept("a shard no new holder had opened was closed")

	var opened []*orderedMem
	for _, h := range holders {
		require.NoError(t, c.node(h).reconcile(t.Context()))
		i := slices.Index(c.ids, peerOf(t, h))
		opened = append(opened, c.shards[i].open[shard])
		require.NotNil(t, opened[len(opened)-1], "holder %s did not open the shard", h)
	}
	kept("a shard its new holders had opened empty was closed")

	opened[0].replicate(written)
	kept("a shard one holder had, of the two a write is acknowledged on, was closed")

	opened[1].replicate(written)
	require.NoError(t, c.nodes[0].reconcile(t.Context()))
	require.NotContains(t, c.shards[0].held(), shard, "a shard its new holders have was kept")
}
