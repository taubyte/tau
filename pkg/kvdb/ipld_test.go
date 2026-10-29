package kvdb

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"testing/synctest"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// fetchedGetter answers GetMany with nodes it already holds.
type fetchedGetter struct {
	ipld.NodeGetter
	nodes []ipld.Node
}

func (g *fetchedGetter) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(g.nodes))
	for _, nd := range g.nodes {
		out <- &ipld.NodeOption{Node: nd}
	}
	close(out)
	return out
}

// TestGetDeltasKeepsWhatWasFetchedInTime: a walk reads a delta, queues it
// for a worker, and reads the next. The channel held one delta, so while the
// walk waited on its workers the next was held back, and once the fetch's
// context expired it was dropped: a block fetched in time was recorded as
// missing and left to the next repair. Under load TestCRDTPriority waited a
// minute for that repair.
func TestGetDeltasKeepsWhatWasFetchedInTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &fetchedGetter{}
		var cids []cid.Cid
		for i := range 3 {
			nd, err := makeNode(&pb.Delta{Priority: uint64(i + 1)}, nil)
			if err != nil {
				t.Fatal(err)
			}
			g.nodes = append(g.nodes, nd)
			cids = append(cids, nd.Cid())
		}

		ctx, cancel := context.WithCancel(t.Context())
		deltas := (&crdtNodeGetter{NodeGetter: g}).GetDeltas(ctx, cids)
		got := 0
		if opt := <-deltas; opt.err != nil {
			t.Fatal(opt.err)
		}
		got++
		// The walk is queueing the first for a worker when the fetch's
		// time runs out.
		synctest.Wait()
		cancel()
		synctest.Wait()
		for opt := range deltas {
			if opt.err != nil {
				t.Fatal(opt.err)
			}
			got++
		}
		if got != len(cids) {
			t.Fatalf("got %d of the %d deltas fetched", got, len(cids))
		}
	})
}

// TestBlockCodecAllocations bounds what a delta block costs to make and to
// read. Reading allocates the delta and its dot, each element and its dot,
// and the element slice as it grows (five times to 16), and copies none of
// the block's bytes; making allocates the data,
// the ProtoNode, the links with the copy SetLinks makes, and the hasher
// SetCidBuilder makes only to check it can (two).
func TestBlockCodecAllocations(t *testing.T) {
	delta := &pb.Delta{Priority: 3, Dot: dot{replica: "replica-id-0000000000000000", counter: 7}.toPB(), At: 1, Ttl: 2}
	for i := range 16 {
		delta.Elements = append(delta.Elements, &pb.Element{
			Key:   fmt.Appendf(nil, "/some/key/%04d", i),
			Value: bytes.Repeat([]byte{byte(i)}, 64),
			Dot:   delta.Dot,
		})
	}
	parent, err := makeNode(&pb.Delta{Priority: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	heads := []Head{{Cid: parent.Cid()}, {Cid: parent.Cid()}}
	nd, err := makeNode(delta, heads)
	if err != nil {
		t.Fatal(err)
	}
	read := testing.AllocsPerRun(200, func() {
		if _, err := extractDelta(nd); err != nil {
			t.Fatal(err)
		}
	})
	made := testing.AllocsPerRun(200, func() {
		if _, err := makeNode(delta, heads); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("read %v, make %v", read, made)
	if read > 2+2*16+5 {
		t.Errorf("reading a block of 16 elements: %v allocations, want at most %d", read, 2+2*16+5)
	}
	if made > 6 {
		t.Errorf("making a block: %v allocations, want at most 6", made)
	}
}

// TestDecodedDeltaAliasesItsBlock: reading a delta copies none of its
// block's bytes.
func TestDecodedDeltaAliasesItsBlock(t *testing.T) {
	nd, err := makeNode(&pb.Delta{Elements: []*pb.Element{{Key: []byte("/a"), Value: []byte("one")}, {Key: []byte("/b"), Value: []byte("two")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := nd.(*dag.ProtoNode).Data()
	delta, err := extractDelta(nd)
	if err != nil {
		t.Fatal(err)
	}
	if v := delta.Elements[1].Value; &v[0] != &data[bytes.Index(data, []byte("two"))] {
		t.Fatal("the value was copied out of the block")
	}
	if k := delta.Elements[1].Key; &k[0] != &data[bytes.Index(data, []byte("/b"))] {
		t.Fatal("the key was copied out of the block")
	}
}

// TestASnapshotMergeReadsEachBlockOnce: a replica catching up fetches the
// blocks of a state, then reads them to check their stamps, to load the
// state and to diff it against its own. Each of those used to read every
// block it needed back from the blockstore and decode it again; the merge
// now reads each block of the state once, the fetch's own read included.
func TestASnapshotMergeReadsEachBlockOnce(t *testing.T) {
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	a, b := replicas[0], replicas[1]
	for i := range 300 {
		put(t, a, fmt.Sprintf("/k/%03d", i), "v")
	}
	compact(t, a)
	counting := &countingDAG{fetchThroughDAGSvc: dagsyncs[1], fetched: map[cid.Cid]int{}}
	b.dagService = counting
	syncReplicaHeads(t, a, b)
	reads := counting.take()
	blocks, again := 0, 0
	for _, level := range stateLevels(t, a) {
		for _, nd := range level {
			blocks++
			if n := reads[nd.Cid()]; n > 1 {
				again++
			}
		}
	}
	t.Logf("%d of the state's %d blocks read more than once", again, blocks)
	if blocks < 10 {
		t.Fatalf("setup: a state of %d blocks", blocks)
	}
	if again != 0 {
		t.Fatalf("%d of the state's %d blocks were read more than once", again, blocks)
	}
}
