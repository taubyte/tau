package kvdb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
	logging "github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/p2p/peer"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

func TestOptionsVerify(t *testing.T) {
	if err := (*Options)(nil).verify(); err == nil {
		t.Fatal("nil options must not verify")
	}
	for name, mutate := range map[string]func(*Options){
		"rebroadcast": func(o *Options) { o.RebroadcastInterval = 0 },
		"logger":      func(o *Options) { o.Logger = nil },
		"workers":     func(o *Options) { o.NumWorkers = 0 },
		"compact":     func(o *Options) { o.CompactAfterBlocks = -1 },
		"dag timeout": func(o *Options) { o.DAGSyncerTimeout = -1 },
		"batch size":  func(o *Options) { o.MaxBatchDeltaSize = 0 },
		"repair":      func(o *Options) { o.RepairInterval = -1 },
		"batch delay": func(o *Options) { o.BroadcastBatchDelay = -1 },
		"clock":       func(o *Options) { o.Clock = nil },
		"uptime":      func(o *Options) { o.Uptime = nil },
		"delivery":    func(o *Options) { o.DeliveryBound = o.DAGSyncerTimeout },
		"skew":        func(o *Options) { o.ClockSkew = -1 },
		"margin":      func(o *Options) { o.Margin = o.minMargin() - 1 },
		"retention":   func(o *Options) { o.Retention = retentionFactor*o.margin() - 1 },
	} {
		o := DefaultOptions()
		mutate(o)
		if err := o.verify(); err == nil {
			t.Errorf("%s: invalid options verified", name)
		}
		if _, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("x"), newFetchThrough(), nil, o); err == nil {
			t.Errorf("%s: a store opened with invalid options", name)
		}
	}
	if err := DefaultOptions().verify(); err != nil {
		t.Fatal(err)
	}
	o := DefaultOptions()
	o.Margin = 2 * o.minMargin()
	o.Retention = retentionFactor * o.Margin
	if err := o.verify(); err != nil {
		t.Fatalf("a margin above the least and a retention of ten margins: %s", err)
	}
}

func TestDecodersRefuseGarbage(t *testing.T) {
	for _, s := range []string{"", "x", "r.1", "r!!.00000000000000000001", "rAA.12", "rAA.abcdefghijklmnopqrst",
		"rAA.0000000000000000001x", "rAA. 0000000000000000001", "rAA.0000000000000000_001", "rAA.+0000000000000000001"} {
		if _, err := parseDotSegment(s); err == nil {
			t.Errorf("parseDotSegment(%q) accepted", s)
		}
	}
	for _, s := range []string{"", "x", "k!!"} {
		if _, err := parseKeySegment(s); err == nil {
			t.Errorf("parseKeySegment(%q) accepted", s)
		}
	}
	if _, err := decodeDot(nil); err == nil {
		t.Error("decodeDot accepted nothing")
	}
	if _, err := decodeHead(cid.Undef, nil); err == nil {
		t.Error("decodeHead accepted nothing")
	}
	if _, err := decodeHead(cid.Undef, []byte{1}); err == nil {
		t.Error("decodeHead accepted a height without a dot")
	}
	if _, err := decodeBlock([]byte{0}); err == nil {
		t.Error("decodeBlock accepted a kind without a dot")
	}
	if _, err := decodeBlock([]byte{0, 0x80}); err == nil {
		t.Error("decodeBlock accepted broken stamps")
	}
	if _, err := decodeBlock([]byte{0, 1, 1, 0x80}); err == nil {
		t.Error("decodeBlock accepted a broken dot")
	}
	if _, err := decodeMarker(nil); err == nil {
		t.Error("decodeMarker accepted nothing")
	}
	if _, _, err := decodeStamps([]byte{1}); err == nil {
		t.Error("decodeStamps accepted one stamp")
	}
	if _, err := decodeLink(dot{}, []byte{1}); err == nil {
		t.Error("decodeLink accepted one stamp")
	}
	if _, err := decodeLink(dot{}, []byte{1, 1, 0xff}); err == nil {
		t.Error("decodeLink accepted a broken cid")
	}
	if _, err := decodePriority(nil); err == nil {
		t.Error("decodePriority accepted nothing")
	}
	if (dot{}).toPB() != nil {
		t.Error("the zero dot has a wire form")
	}
	if !(newCausalContext()).covers(dot{}) {
		t.Error("the zero dot names nothing, so it is always covered")
	}

	s := newSet(nil, ds.NewKey("/s"))
	for _, k := range []string{"/s/e/nokey", "/s/e/x/r.1", "/s/e/" + keySegment("/a") + "/bad"} {
		if _, err := s.parseElem(k, []byte{1}); err == nil {
			t.Errorf("parseElem(%q) accepted", k)
		}
	}
	if _, err := s.parseElem("/s/e/"+keySegment("/a")+"/"+(dot{replica: "r", counter: 1}).segment(), nil); err == nil {
		t.Error("parseElem accepted an element without a priority")
	}
	for _, k := range []string{"/s/t/nokey", "/s/t/" + (dot{replica: "r", counter: 1}).segment() + "/x"} {
		if _, err := s.parsePending(k, []byte{1, 1}); err == nil {
			t.Errorf("parsePending(%q) accepted", k)
		}
	}
	if _, err := s.parsePending("/s/t/"+(dot{replica: "r", counter: 1}).segment()+"/"+keySegment("/a"), nil); err == nil {
		t.Error("parsePending accepted a removal without stamps")
	}

	if _, err := extractDelta(&fakeRawNode{}); err == nil {
		t.Error("extractDelta accepted a node that is not a ProtoNode")
	}
	if _, err := extractDelta(dag.NodeWithData([]byte{0xff, 0xff})); err == nil {
		t.Error("extractDelta accepted garbage")
	}
	r := &Datastore{logger: DefaultOptions().Logger}
	if _, err := r.decodeBroadcast([]byte{0xff}); err == nil {
		t.Error("decodeBroadcast accepted garbage")
	}
	bad, err := proto.Marshal(&pb.CRDTBroadcast{Heads: []*pb.Head{{Cid: []byte{1, 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.decodeBroadcast(bad); err == nil {
		t.Error("decodeBroadcast accepted a broken cid")
	}
}

type fakeRawNode struct{ ipld.Node }

func TestContextAbsorbsAcrossGaps(t *testing.T) {
	c := newCausalContext()
	a := func(n uint64) dot { return dot{replica: "a", counter: n} }
	spans := func(c *causalContext, r string) []span { return c.inc[r].spans }

	c.add(a(5), 4, 100, 200)
	if got := spans(c, "a"); len(got) != 1 || got[0] != (span{4, 5}) || !c.covers(a(5)) || c.covers(a(4)) {
		t.Fatalf("an incarnation first seen at 5 is known from its prev, 4: %v", got)
	}
	c.add(a(9), 7, 50, 300)
	if got := spans(c, "a"); len(got) != 2 || !c.covers(a(9)) || c.covers(a(7)) || c.covers(a(6)) {
		t.Fatalf("a dot past a gap waits in the cloud: %v", got)
	}
	if !c.covers(a(8)) {
		t.Fatal("8 is a counter the author skipped after 7: covering it is harmless, and the span does")
	}
	c.add(a(7), 5, 10, 10)
	if got := spans(c, "a"); len(got) != 1 || got[0] != (span{4, 9}) {
		t.Fatalf("the chain 5,7,9 should join into one entry: %v", got)
	}
	c.add(a(4), 0, 10, 10)
	if got := spans(c, "a"); len(got) != 1 || got[0] != (span{0, 9}) || c.counter("a") != 9 {
		t.Fatalf("a dot below the base extends the entry down: %v", got)
	}
	if k := c.inc["a"]; k.changed != 100 || k.expires != 300 {
		t.Fatalf("stamps join by max: changed %d expires %d", k.changed, k.expires)
	}
	c.add(a(3), 2, 1, 1)
	if got := spans(c, "a"); len(got) != 1 || got[0] != (span{0, 9}) {
		t.Fatalf("a covered dot changes nothing: %v", got)
	}
	c.touch("a", 400, 500)
	c.touch("zz", 400, 500)
	if k := c.inc["a"]; k.changed != 400 || k.expires != 500 || c.knows("zz") {
		t.Fatal("touch moves a known incarnation's stamps and opens nothing")
	}

	o := newCausalContext()
	o.add(a(20), 15, 1, 1)
	o.add(dot{replica: "b", counter: 1}, 0, 2, 2)
	c.merge(o)
	back := contextFromPB(c.toPB())
	if got := spans(back, "a"); len(got) != 2 || got[0] != (span{0, 9}) || got[1] != (span{15, 20}) || back.counter("b") != 1 {
		t.Fatalf("context did not round-trip: %v", back.toPB())
	}
	if back.counter("nobody") != 0 {
		t.Fatal("an unknown incarnation has no counter")
	}
	// The join keeps the lowest span as the entry whichever side held it.
	x, y := newCausalContext(), newCausalContext()
	x.add(a(12), 10, 1, 1)
	y.add(a(2), 0, 1, 1)
	x.merge(y)
	y.add(a(12), 10, 1, 1)
	if x.toPB().String() != y.toPB().String() {
		t.Fatalf("the same coverage in two orders reads differently:\n%v\n%v", x.toPB(), y.toPB())
	}

	odd := contextFromPB(&pb.Context{
		Vector: []*pb.Entry{{Replica: []byte("e"), Base: 3, Counter: 3}},
		Cloud:  []*pb.Span{{Replica: []byte("nobody"), Lo: 1, Hi: 2}},
	})
	if len(odd.inc) != 0 {
		t.Fatalf("an empty entry and a span with no entry were kept: %v", odd.toPB())
	}
}

func TestTxnReadsItsOwnWrites(t *testing.T) {
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	if err := store.Put(ctx, ds.NewKey("/a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	tx := newTxn(store)
	tx.del(ds.NewKey("/a"))
	tx.put(ds.NewKey("/b"), nil)
	if _, err := tx.get(ctx, ds.NewKey("/a")); !errors.Is(err, ds.ErrNotFound) {
		t.Fatalf("a deleted key read %v", err)
	}
	if v, err := tx.get(ctx, ds.NewKey("/b")); err != nil || len(v) != 0 {
		t.Fatalf("a written key read %q %v", v, err)
	}
	if has, err := tx.has(ctx, ds.NewKey("/a")); err != nil || has {
		t.Fatalf("a deleted key exists: %v %v", has, err)
	}

	plain := &unbatched{Datastore: store}
	if err := tx.commit(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := batchOf(ctx, plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, ds.NewKey("/c"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if has, _ := store.Has(ctx, ds.NewKey("/c")); !has {
		t.Fatal("a store without batches did not get the write")
	}

	faulty := newFaultyDatastore(ds.NewMapDatastore())
	faulty.SetFail(failAlways("Query"))
	if err := scanStore(ctx, faulty, ds.NewKey("/"), func(string, []byte) error { return nil }); !errors.Is(err, errFault) {
		t.Fatalf("scan over a failing store: %v", err)
	}
	if _, err := newTxn(faulty).scan(ctx, ds.NewKey("/")); !errors.Is(err, errFault) {
		t.Fatalf("txn scan over a failing store: %v", err)
	}
	if _, err := countPrefix(ctx, faulty, ds.NewKey("/")); !errors.Is(err, errFault) {
		t.Fatalf("count over a failing store: %v", err)
	}
	faulty.SetFail(failAlways("Batch"))
	tx = newTxn(faulty)
	tx.put(ds.NewKey("/x"), []byte("1"))
	if err := tx.commit(ctx); !errors.Is(err, errFault) {
		t.Fatalf("commit over a failing store: %v", err)
	}
	for _, op := range []string{"BatchPut", "BatchDelete"} {
		faulty.SetFail(failAlways(op))
		tx = newTxn(faulty)
		tx.put(ds.NewKey("/x"), []byte("1"))
		tx.del(ds.NewKey("/y"))
		if err := tx.commit(ctx); !errors.Is(err, errFault) {
			t.Fatalf("%s: %v", op, err)
		}
	}
}

// unbatched hides a store's batches.
type unbatched struct{ ds.Datastore }

func openOn(t *testing.T, store ds.Datastore, d ipld.DAGService, o *Options) (*Datastore, error) {
	t.Helper()
	if o == nil {
		o = noAutoCompact()
	}
	o.Logger = &testLogger{name: "f: ", l: DefaultOptions().Logger}
	o.ReplicaID = []byte("f")
	return NewDatastore(store, ds.NewKey("crdttest"), d, &nullBroadcaster{}, o)
}

func TestOpenFailsWithItsStore(t *testing.T) {
	for _, op := range []string{"Get", "Query", "Has", "Put", "Sync"} {
		store := newFaultyDatastore(ds.NewMapDatastore())
		store.SetFail(failAlways(op))
		if _, err := openOn(t, store, newFetchThrough(), nil); err == nil {
			t.Errorf("open succeeded with %s failing", op)
		}
	}
}

func TestAnotherFormatIsEmptied(t *testing.T) {
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	ns := ds.NewKey("crdttest")
	if err := store.Put(ctx, ns.ChildString(versionKey), []byte{3}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, ns.ChildString("s/t/old/tomb"), nil); err != nil {
		t.Fatal(err)
	}
	r, err := openOn(t, store, newFetchThrough(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if has, _ := store.Has(ctx, ns.ChildString("s/t/old/tomb")); has {
		t.Fatal("a store in the v2 format kept its v2 entries")
	}

	faulty := newFaultyDatastore(ds.NewMapDatastore())
	if err := faulty.Put(ctx, ns.ChildString(versionKey), []byte{2}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"Query", "Batch"} {
		faulty.SetFail(func(o string, k ds.Key) error {
			if o == op && !strings.Contains(k.String(), versionKey) {
				return errFault
			}
			return nil
		})
		if _, err := openOn(t, faulty, newFetchThrough(), nil); err == nil {
			t.Errorf("emptying succeeded with %s failing", op)
		}
	}
}

// addFailing is a DAG service whose Add fails, and whose RemoveMany can be
// made to.
type addFailing struct {
	*fetchThroughDAGSvc
	addFails    atomic.Bool
	removeFails atomic.Bool
}

func (a *addFailing) Add(ctx context.Context, n ipld.Node) error {
	if a.addFails.Load() {
		return errFault
	}
	return a.fetchThroughDAGSvc.Add(ctx, n)
}

func (a *addFailing) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	if a.removeFails.Load() {
		return errFault
	}
	return a.fetchThroughDAGSvc.RemoveMany(ctx, cids)
}

func TestAFailedWriteLeavesNothingBehind(t *testing.T) {
	ctx := context.Background()
	store := newFaultyDatastore(ds.NewMapDatastore())
	d := &addFailing{fetchThroughDAGSvc: newFetchThrough()}
	r, err := openOn(t, store, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	put(t, r, "/a", "1")

	d.addFails.Store(true)
	if err := r.Put(ctx, ds.NewKey("/b"), []byte("2")); !errors.Is(err, errFault) {
		t.Fatalf("a put whose block cannot be written: %v", err)
	}
	if _, err := r.Compact(ctx); !errors.Is(err, errFault) {
		t.Fatalf("a compaction whose blocks cannot be written: %v", err)
	}
	d.addFails.Store(false)
	assertNoLeak(t, r, d.fetchThroughDAGSvc)

	store.SetFail(func(op string, k ds.Key) error {
		if op == "BatchPut" && strings.Contains(k.String(), "/"+contextNs+"/") {
			return errFault
		}
		return nil
	})
	if err := r.Put(ctx, ds.NewKey("/c"), []byte("3")); !errors.Is(err, errFault) {
		t.Fatalf("a put whose merge cannot commit: %v", err)
	}
	store.SetFail(nil)
	assertNoLeak(t, r, d.fetchThroughDAGSvc)
	if r.counter != 1 {
		t.Fatalf("a failed write must not consume a counter, have %d", r.counter)
	}
	put(t, r, "/c", "3")
	want(t, "r", r, map[string]string{"/a": "1", "/b": "<deleted>", "/c": "3"})

	// A discard that cannot remove its blocks leaves them to the next open.
	d.removeFails.Store(true)
	store.SetFail(func(op string, k ds.Key) error {
		if op == "BatchPut" && strings.Contains(k.String(), "/"+contextNs+"/") {
			return errFault
		}
		return nil
	})
	if err := r.Put(ctx, ds.NewKey("/d"), []byte("4")); err == nil {
		t.Fatal("the put should fail")
	}
	store.SetFail(failAlways("Batch"))
	r.discardBlocks(ctx, cid.Undef)
	d.removeFails.Store(false)
	store.SetFail(nil)
	if err := r.dropOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoLeak(t, r, d.fetchThroughDAGSvc)
}

func TestWritesFailWithTheirStore(t *testing.T) {
	ctx := context.Background()
	store := newFaultyDatastore(ds.NewMapDatastore())
	r, err := openOn(t, store, newFetchThrough(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	put(t, r, "/a", "1")
	store.SetFail(failAlways("Query"))
	if err := r.Put(ctx, ds.NewKey("/a"), []byte("2")); !errors.Is(err, errFault) {
		t.Fatalf("put: %v", err)
	}
	if _, err := r.Compact(ctx); !errors.Is(err, errFault) {
		t.Fatalf("compact: %v", err)
	}
	if err := r.repairDAG(ctx); !errors.Is(err, errFault) {
		t.Fatalf("repair: %v", err)
	}
	if _, err := r.purge(ctx, r.cc); !errors.Is(err, errFault) {
		t.Fatalf("purge: %v", err)
	}
	if err := r.dropOrphans(ctx); !errors.Is(err, errFault) {
		t.Fatalf("dropOrphans: %v", err)
	}
	store.SetFail(failAlways("Put", "Delete", "Has"))
	r.MarkDirty(ctx)
	r.MarkClean(ctx)
	if r.IsDirty(ctx) {
		t.Fatal("a failing store reads as dirty")
	}
	store.SetFail(failAlways("Has"))
	if _, err := r.isIndexed(ctx, cid.Undef); !errors.Is(err, errFault) {
		t.Fatalf("isIndexed: %v", err)
	}
	if err := r.handleBlock(ctx, Head{Cid: cid.Undef}); !errors.Is(err, errFault) {
		t.Fatalf("handleBlock: %v", err)
	}
	store.SetFail(nil)
}

func TestMergeRefusesMalformedBlocks(t *testing.T) {
	ctx := context.Background()
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	r := replicas[0]
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := r.mergeDelta(ctx, cid.Undef, &pb.Delta{}); err == nil {
		t.Error("a delta without a dot merged")
	}
	if _, _, err := r.mergeSnapshot(ctx, cid.Undef, &pb.Delta{Snapshot: true}, r.dagService); err == nil {
		t.Error("a snapshot without a dot merged")
	}
	d := dot{replica: "x", counter: 1}
	if _, _, err := r.mergeSnapshot(ctx, cid.Undef, &pb.Delta{Snapshot: true, Dot: d.toPB()}, r.dagService); err == nil {
		t.Error("a snapshot without a state merged")
	}
	noDot := elemTreeKey("/k", dot{replica: "x"})
	state := putState(t, r.dagService, map[string][]byte{string(noDot): elemValue(element{})}, nil, nil)
	if _, _, err := r.mergeSnapshot(ctx, cid.Undef, &pb.Delta{Snapshot: true, Dot: d.toPB(), State: state.Bytes()}, r.dagService); err == nil {
		t.Error("a snapshot holding an element with no dot merged")
	}
	if _, err := r.mergeDelta(ctx, cid.Undef, &pb.Delta{Dot: d.toPB(), Parents: []*pb.Parent{{Cid: []byte{1}}}}); err == nil {
		t.Error("a delta with a broken parent merged")
	}
}

func TestAChunkIsNotADelta(t *testing.T) {
	ctx := context.Background()
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	r := replicas[0]
	chunk := &pb.Delta{Dot: dot{replica: "x", counter: 1}.toPB(), Elements: []*pb.Element{{Key: "/k", Dot: dot{replica: "x", counter: 1}.toPB()}}}
	nd, err := makeNode(chunk, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.processNode(ctx, r.currentGeneration(), &crdtNodeGetter{r.dagService}, chunk, nd); err == nil {
		t.Fatal("a chunk merged as a delta")
	}
}

func TestASnapshotWithAMissingStateIsNotMerged(t *testing.T) {
	ctx := context.Background()
	replicas, _, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	r := replicas[0]
	root := &pb.Delta{Snapshot: true, Dot: dot{replica: "x", counter: 1}.toPB(), State: []byte{1, 2}}
	f, err := r.fetchState(ctx, &crdtNodeGetter{r.dagService}, cid.Undef, root)
	if f != nil || err == nil {
		t.Fatal("a broken state cid was accepted")
	}
	missing, err := stateRoot{}.node()
	if err != nil {
		t.Fatal(err)
	}
	root.State = missing.Cid().Bytes()
	cctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	f, err = r.fetchState(cctx, &crdtNodeGetter{r.dagService}, cid.Undef, root)
	r.leaveFetch(f, true)
	if err == nil {
		t.Fatal("a state nobody holds was fetched")
	}
}

type errBroadcaster struct{ nullBroadcaster }

func (errBroadcaster) Broadcast(context.Context, []byte) error { return errFault }

// TestAStoreWithABroadcasterNeedsAReplicaID: without one, every restart of a
// store is a new peer, which every judge waits G for, so one that crash-loops
// holds every judge back for ever. An offline store hears no peer.
func TestAStoreWithABroadcasterNeedsAReplicaID(t *testing.T) {
	o := DefaultOptions()
	if _, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("x"), newFetchThrough(), &nullBroadcaster{}, o); err == nil {
		t.Fatal("a store with a broadcaster opened without a ReplicaID")
	}
	r, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("x"), newFetchThrough(), nil, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBroadcasting(t *testing.T) {
	ctx := context.Background()
	o := noAutoCompact()
	o.Logger = DefaultOptions().Logger
	o.ReplicaID = []byte("b")
	r, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("b"), newFetchThrough(), &errBroadcaster{}, o)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Put(ctx, ds.NewKey("/a"), []byte("1")); !errors.Is(err, errFault) {
		t.Fatalf("a put that cannot be announced: %v", err)
	}
	if _, err := r.Compact(ctx); !errors.Is(err, errFault) {
		t.Fatalf("a compaction that cannot be announced: %v", err)
	}
	r.rebroadcastHeads(ctx)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.broadcast(cancelled, Head{Cid: onlyHead(t, r)}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("broadcast on a cancelled context: %v", err)
	}
	if err := r.broadcast(ctx, Head{}, nil); err != nil {
		t.Fatal(err)
	}
	// A batching replica is built as one: its batch worker reads the delay
	// when it starts, so changing it on a running replica races that read.
	batching := noAutoCompact()
	batching.Logger = o.Logger
	batching.ReplicaID = []byte("b")
	batching.BroadcastBatchDelay = time.Hour
	batched, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("b"), newFetchThrough(), &errBroadcaster{}, batching)
	if err != nil {
		t.Fatal(err)
	}
	defer batched.Close()
	if err := batched.broadcast(ctx, Head{Cid: onlyHead(t, r)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.InternalStats(ctx); len(got.Heads) != 1 || got.MaxHeight == 0 {
		t.Fatalf("stats: %+v", got)
	}

	offline, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("b"), newFetchThrough(), nil, o)
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close()
	if err := offline.broadcast(ctx, Head{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := offline.broadcastHeads(ctx, []Head{{}}); err != nil {
		t.Fatal(err)
	}
}

func TestBatchCommitsWhenItOutgrowsItsDelta(t *testing.T) {
	ctx := context.Background()
	o := noAutoCompact()
	o.MaxBatchDeltaSize = 16
	replicas, _, done := makeNReplicasSeparateStores(t, 1, o)
	defer done()
	r := replicas[0]
	b, err := r.Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, ds.NewKey("/a"), bytes.Repeat([]byte("x"), 32)); err != nil {
		t.Fatal(err)
	}
	if has, _ := r.Has(ctx, ds.NewKey("/a")); !has {
		t.Fatal("an oversized batch should have committed on its own")
	}
	if err := b.Put(ctx, ds.NewKey("/b"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, ds.NewKey("/b"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	if err := b.Delete(ctx, ds.NewKey("/a")); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	want(t, "r", r, map[string]string{"/a": "<deleted>", "/b": "2"})
}

func TestRepairReportsWhatItCannotFetch(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	w, r := replicas[0], replicas[1]
	put(t, w, "/a", "1")
	head := put(t, w, "/b", "2")
	mergeOne(t, r, head)
	dagsyncs[1].setRemotes()
	if err := r.Repair(ctx); err == nil {
		t.Fatal("a repair that could fetch nothing succeeded")
	}
	if !r.IsDirty(ctx) {
		t.Fatal("a failed repair leaves the store dirty")
	}
	from(dagsyncs, 1, 0)
	if err := r.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := r.DotDAG(ctx, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "digraph") {
		t.Fatal(out.String())
	}
	if err := r.PrintDAG(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDotDAGShowsBlocksItDoesNotHold(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	w, r := replicas[0], replicas[1]
	put(t, w, "/a", "1")
	mergeOne(t, r, put(t, w, "/b", "2"))
	dagsyncs[1].setRemotes()
	var out bytes.Buffer
	if err := r.DotDAG(ctx, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not held") {
		t.Fatal(out.String())
	}
}

func TestAdoptingHeadsBeforeSyncing(t *testing.T) {
	ctx := context.Background()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 2, noAutoCompact())
	defer done()
	w, r := replicas[0], replicas[1]
	head := put(t, w, "/a", "1")
	hs, _ := w.heads.List()
	r.adoptHeads(ctx, hs)
	if _, ok := r.heads.Get(head); !ok {
		t.Fatal("the heard head was not adopted")
	}
	put(t, r, "/b", "2")
	if err := r.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	want(t, "r", r, map[string]string{"/a": "1", "/b": "2"})
	dagsyncs[1].setRemotes()
	r.adoptHeads(ctx, []Head{{Cid: cid.NewCidV1(cid.DagProtobuf, head.Hash())}})
}

func TestKVDatabaseSurface(t *testing.T) {
	ctx := context.Background()
	node := peer.Mock(ctx)
	defer node.Close()
	f := New(node)
	db, err := f.New(logging.Logger("kv"), "surface", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Factory() != f {
		t.Fatal("factory")
	}
	if err := db.Put(ctx, "/x/a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "/x/a"); err != nil {
		t.Fatal(err)
	}
	if n, err := db.(interface {
		Compact(context.Context) (int, error)
	}).Compact(ctx); err != nil || n == 0 {
		t.Fatalf("compact dropped %d: %v", n, err)
	}
	if err := db.Put(ctx, "/x/b", []byte("2")); err != nil {
		t.Fatal(err)
	}
	keys, err := db.ListRegEx(ctx, "/x", "b$")
	if err != nil || len(keys) != 1 {
		t.Fatalf("regex list: %v %v", keys, err)
	}
	if _, err := db.ListRegEx(ctx, "/x", "("); err == nil {
		t.Fatal("a broken regex listed")
	}
	if st := db.Stats(ctx); len(st.Heads()) != 1 {
		t.Fatalf("stats heads %v", st.Heads())
	}
	if NewStats().Type() != 0 {
		t.Fatal("type")
	}
	held, err := f.(*factory).Holding(ctx, "surface")
	if err != nil || !held {
		t.Fatalf("holding: %v %v", held, err)
	}
	paths, err := f.(*factory).Paths(ctx, "", 1)
	if err != nil || len(paths) == 0 {
		t.Fatalf("paths: %v %v", paths, err)
	}
}
