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

// A block can be asked for only once a block naming it has arrived, so its
// round is one past the latest earlier block whose bytes hold its cid.
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

func carriedBy(root *pb.Delta) map[cid.Cid]bool {
	out := map[cid.Cid]bool{cid.MustParse(root.GetState()): true}
	for _, raw := range root.GetInline() {
		out[cidOf(sha256.Sum256(raw))] = true
	}
	return out
}

// A tree's shape turns on the incarnation in its keys, so widePeer draws
// replicas until one leaves at least fetch blocks to fetch.
func widePeer(t testing.TB, name string, n, fetch int) (*Datastore, *fetchThroughDAGSvc) {
	t.Helper()
	for {
		peerDag := newFetchThrough()
		peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, name)
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

	// The trees share structure, so a block of the older snapshot can reappear in the
	// newer one, linked; the stranger is one that does not.
	carried := map[string]bool{}
	for _, b := range all {
		carried[string(b)] = true
	}
	var stranger []byte
	for _, b := range oldBlocks {
		if !carried[string(b)] {
			stranger = b
			break
		}
	}
	if stranger == nil {
		t.Fatal("every block of the older snapshot is in the newer one")
	}

	flipped := slices.Clone(good.GetInline()[1])
	flipped[len(flipped)/2] ^= 1
	type testCase struct {
		edit func(d *pb.Delta)
		want error
	}
	cases := map[string]testCase{
		"no root":        {func(d *pb.Delta) { d.Root = nil }, errStateMismatch},
		"swapped trees":  {func(d *pb.Delta) { d.Trees[0], d.Trees[1] = d.Trees[1], d.Trees[0] }, errStateMismatch},
		"missing tree":   {func(d *pb.Delta) { d.Root.Context, d.Trees = false, d.Trees[:1] }, errStateMismatch},
		"other format":   {func(d *pb.Delta) { d.Root.Format = stateFormat + 1 }, errMalformedState},
		"extra tree":     {func(d *pb.Delta) { d.Trees = append(d.Trees, d.Trees[0]) }, errMalformedState},
		"wrong bytes":    {func(d *pb.Delta) { d.Inline[1] = flipped }, errInlineUnlinked},
		"unlinked block": {func(d *pb.Delta) { d.Inline = append(d.Inline[:2:2], stranger) }, errInlineUnlinked},
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
