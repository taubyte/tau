package kvdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// roundDAG is a peer that records every block it serves, in order.
type roundDAG struct {
	ipld.DAGService

	mu     sync.Mutex
	served []ipld.Node
}

func (r *roundDAG) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	nd, err := r.DAGService.Get(ctx, c)
	if err == nil {
		r.mu.Lock()
		r.served = append(r.served, nd)
		r.mu.Unlock()
	}
	return nd, err
}

// rounds is how many round trips to the peer the blocks it served took at
// the least: a block can be asked for only once a block naming it has
// arrived, so its round is one past the latest round of a block served
// before it whose bytes hold its cid, or the first.
func (r *roundDAG) rounds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	round := make([]int, len(r.served))
	most := 0
	for i, nd := range r.served {
		round[i] = 1
		name := nd.Cid().Bytes()
		for j := range i {
			if bytes.Contains(r.served[j].RawData(), name) {
				round[i] = max(round[i], round[j]+1)
			}
		}
		most = max(most, round[i])
	}
	return most
}

func (r *roundDAG) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.served = nil
}

// carriedBy is the blocks the snapshot root carries: its StateRoot and its
// inline blocks.
func carriedBy(root *pb.Delta) map[cid.Cid]bool {
	out := map[cid.Cid]bool{cid.MustParse(root.GetState()): true}
	for _, raw := range root.GetInline() {
		out[cidOf(sha256.Sum256(raw))] = true
	}
	return out
}

// widePeer is a replica that compacted n keys of wide values once, into a
// snapshot that leaves at least fetch blocks of its state for a reader to
// fetch. A tree's shape turns on the incarnation in its keys, so it draws
// replicas until one does.
func widePeer(t testing.TB, n, fetch int) (*Datastore, *fetchThroughDAGSvc) {
	t.Helper()
	for {
		peerDag := newFetchThrough()
		peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
		seedWide(t, peer, n, "p")
		compact(t, peer)
		if len(stateClosure(t, peer)) >= fetch {
			return peer, peerDag
		}
		if err := peer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// seedWide writes n keys of values wide enough that the state they make is
// larger than a snapshot carries, in one batch.
func seedWide(t testing.TB, r *Datastore, n int, prefix string) {
	t.Helper()
	ctx := context.Background()
	b, err := r.Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		v := fmt.Appendf(bytes.Repeat([]byte{'w'}, 800), "%d", i)
		if err := b.Put(ctx, ds.NewKey(fmt.Sprintf("/%s%04d", prefix, i)), v); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestAWarmCatchUpOfOneKeyTakesOneRound: a replica one snapshot behind, at
// 1000 keys with one changed since, gets the snapshot and nothing else:
// the StateRoot is rebuilt from what the snapshot carries, and the tree
// nodes on the changed paths are carried inline. Fetching the StateRoot,
// then each level of the trees from its parent, took five.
func TestAWarmCatchUpOfOneKeyTakesOneRound(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	peer := &roundDAG{DAGService: dagsyncs[0].DAGService}
	dagsyncs[1].setRemotes(peer)
	seedKeys(t, a, 1000, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	if b.IsDirty(ctx) {
		t.Fatal("the first catch-up did not finish")
	}

	put(t, a, "/k0007", "again")
	compact(t, a)
	peer.reset()
	syncReplicaHeads(t, a, b)
	if b.IsDirty(ctx) {
		t.Fatal("the catch-up did not finish")
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	if n := peer.rounds(); n > 1 {
		t.Fatalf("the catch-up took %d rounds for %d blocks, want 1", n, len(peer.served))
	}
	assertReach(t, b)
	assertNoLeak(t, b, dagsyncs[1])
}

// TestASmallStateArrivesWithItsSnapshot: a state within the budget is
// carried whole, so a replica that holds none of it fetches the snapshot
// only, and holds the state as a fetched one is held.
func TestASmallStateArrivesWithItsSnapshot(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	peer := &roundDAG{DAGService: dagsyncs[0].DAGService}
	dagsyncs[1].setRemotes(peer)
	seedKeys(t, a, 40, "k")
	compact(t, a)
	syncReplicaHeads(t, a, b)
	if b.IsDirty(ctx) {
		t.Fatal("the catch-up did not finish")
	}
	if got, want := queryAll(t, b), queryAll(t, a); !sameKV(got, want) {
		t.Fatalf("b holds %d keys, want %d", len(got), len(want))
	}
	if len(peer.served) != 1 {
		t.Fatalf("b fetched %d blocks, want the snapshot only", len(peer.served))
	}
	if err := b.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	assertTreesMatch(t, b)
	assertReach(t, b)
	assertNoLeak(t, b, dagsyncs[1])
}

// headSnapshot is r's only head, a snapshot, decoded into a delta of its
// own bytes.
func headSnapshot(t testing.TB, r *Datastore) (cid.Cid, *pb.Delta) {
	t.Helper()
	c := onlyHead(t, r)
	_, root, err := (&crdtNodeGetter{r.dagService}).GetDelta(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if !root.GetSnapshot() {
		t.Fatal("the head is not a snapshot")
	}
	return c, proto.Clone(root).(*pb.Delta)
}

// treeBlocks is every block of the trees of r's head state, breadth first
// from their roots.
func treeBlocks(t testing.TB, r *Datastore, root *pb.Delta) [][]byte {
	t.Helper()
	var out [][]byte
	var queue []cid.Cid
	for _, b := range root.GetTrees() {
		queue = append(queue, cid.MustParse(b))
	}
	for len(queue) > 0 {
		nd, err := r.dagService.Get(context.Background(), queue[0])
		if err != nil {
			t.Fatal(err)
		}
		queue = queue[1:]
		out = append(out, nd.RawData())
		queue = append(queue, blockLinksOf(t, nd)...)
	}
	return out
}

// TestATamperedCarriedStateIsRefused: a snapshot whose carried StateRoot
// is not its state, or whose inline blocks are not each linked by one
// before it, or total more than the budget, is refused before any fetch
// begins.
func TestATamperedCarriedStateIsRefused(t *testing.T) {
	ctx := context.Background()
	replicas, _, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	seedKeys(t, a, 50, "old")
	compact(t, a)
	_, older := headSnapshot(t, a)
	oldBlocks := treeBlocks(t, a, older)
	seedKeys(t, a, 1000, "k")
	compact(t, a)
	c, good := headSnapshot(t, a)
	// No removal is pending: the trees are the elements and the context.
	if len(good.GetInline()) < 4 || len(good.GetTrees()) != 2 {
		t.Fatalf("the snapshot carries %d trees and %d blocks", len(good.GetTrees()), len(good.GetInline()))
	}
	all := treeBlocks(t, a, good)

	flipped := slices.Clone(good.GetInline()[1])
	flipped[len(flipped)/2] ^= 1
	cases := map[string]struct {
		edit func(d *pb.Delta)
		want error
	}{
		"no root":        {func(d *pb.Delta) { d.Root = nil }, errStateMismatch},
		"swapped trees":  {func(d *pb.Delta) { d.Trees[0], d.Trees[1] = d.Trees[1], d.Trees[0] }, errStateMismatch},
		"missing tree":   {func(d *pb.Delta) { d.Root.Context, d.Trees = false, d.Trees[:1] }, errStateMismatch},
		"other format":   {func(d *pb.Delta) { d.Root.Format = stateFormat + 1 }, errMalformedState},
		"extra tree":     {func(d *pb.Delta) { d.Trees = append(d.Trees, d.Trees[0]) }, errMalformedState},
		"wrong bytes":    {func(d *pb.Delta) { d.Inline[1] = flipped }, errInlineUnlinked},
		"unlinked block": {func(d *pb.Delta) { d.Inline = append(d.Inline[:2:2], oldBlocks[0]) }, errInlineUnlinked},
		"child first":    {func(d *pb.Delta) { d.Inline[0], d.Inline[3] = d.Inline[3], d.Inline[0] }, errInlineUnlinked},
		"twice":          {func(d *pb.Delta) { d.Inline = append(d.Inline[:2:2], d.Inline[0]) }, errInlineUnlinked},
		"oversize":       {func(d *pb.Delta) { d.Inline = all }, errInlineOversize},
	}
	for name, tc := range cases {
		d := proto.Clone(good).(*pb.Delta)
		tc.edit(d)
		f, err := b.fetchState(ctx, &crdtNodeGetter{b.dagService}, c, d, nil)
		if f != nil || !errors.Is(err, tc.want) {
			t.Errorf("%s: fetch %v, error %v, want %v", name, f != nil, err, tc.want)
		}
	}
	if n := fetchesInProgress(b); n != 0 {
		t.Fatalf("%d fetches begun for refused snapshots", n)
	}
	f, err := b.fetchState(ctx, &crdtNodeGetter{b.dagService}, c, good, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.leaveFetch(ctx, f, false)
}
