package kvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	ipld "github.com/ipfs/go-ipld-format"
)

type removeFailing struct {
	*fetchThroughDAGSvc
	fail atomic.Bool
}

func (r *removeFailing) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	if r.fail.Load() {
		return errFault
	}
	return r.fetchThroughDAGSvc.RemoveMany(ctx, cids)
}

func (r *removeFailing) Remove(ctx context.Context, c cid.Cid) error {
	if r.fail.Load() {
		return errFault
	}
	return r.fetchThroughDAGSvc.Remove(ctx, c)
}

var _ ipld.DAGService = (*removeFailing)(nil)

func assertNoLeak(t testing.TB, r *Datastore, d *fetchThroughDAGSvc) {
	t.Helper()
	ctx := context.Background()
	indexed := map[cid.Cid]bool{}
	prefix := r.blocksPrefix()
	err := scanStore(ctx, r.store, prefix, func(k string, v []byte) error {
		c, err := cidOfBlockKey(prefix, k)
		if err != nil {
			return err
		}
		b, err := decodeBlock(v)
		if err != nil {
			return err
		}
		bd := b.dot
		if !r.covered(bd) {
			t.Errorf("index entry %s has a dot %s nothing merged", c, bd)
		}
		indexed[c] = true
		if !d.holds(c) {
			t.Errorf("index entry %s names a block that is gone", c)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for c := range owned(t, r) {
		indexed[c] = true
		if !d.holds(c) {
			t.Errorf("state block %s a root reaches is gone", c)
		}
	}
	ch, err := d.bs.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for c := range ch {
		if !indexed[cid.NewCidV1(cid.Raw, c.Hash())] {
			t.Errorf("block %s is held and indexed nowhere: a leak", c)
		}
	}
}

func reopen(t testing.TB, old *Datastore, store ds.Datastore, dag ipld.DAGService, name string) *Datastore {
	t.Helper()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	// Closing cleared the bad-shutdown mark; a crash would have left it.
	if err := store.Put(context.Background(), ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
		t.Fatal(err)
	}
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(old.incarnations, replicaOptions(noAutoCompact(), name)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCrashMidCompaction(t *testing.T) {
	for _, window := range []string{"before the merge", "before the purge"} {
		t.Run(window, func(t *testing.T) {
			ctx := context.Background()
			store := newFaultyDatastore(ds.NewMapDatastore())
			dag := &removeFailing{fetchThroughDAGSvc: newFetchThrough()}
			r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(aNode(), replicaOptions(noAutoCompact(), "crash: ")))
			if err != nil {
				t.Fatal(err)
			}
			control, cdags, closeControl := makeNReplicasSeparateStores(t, 1, noAutoCompact())
			defer closeControl()
			cdags[0].setRemotes(dag.DAGService)

			for i := range 12 {
				put(t, r, fmt.Sprintf("k%d", i%5), fmt.Sprintf("v%d", i))
				if i%3 == 0 {
					del(t, r, fmt.Sprintf("k%d", i%5))
				}
			}
			syncReplicaHeads(t, r, control[0])
			before := r.blockCount

			dag.fail.Store(true)
			if window == "before the merge" {
				store.SetFail(func(op string, key ds.Key) error {
					if op == "BatchPut" && strings.Contains(key.String(), "/"+contextNs+"/") {
						return errFault
					}
					return nil
				})
			}
			if _, err := r.Compact(ctx); !errors.Is(err, errFault) {
				t.Fatalf("the compaction should have died, got %v", err)
			}
			store.SetFail(nil)
			dag.fail.Store(false)

			r = reopen(t, r, store, dag, "reopened: ")
			defer r.Close()
			if window == "before the merge" {
				assertNoLeak(t, r, dag.fetchThroughDAGSvc)
				if r.blockCount != before {
					t.Fatalf("reopen should leave the %d blocks from before the compaction, has %d", before, r.blockCount)
				}
			}

			compact(t, r)
			assertNoLeak(t, r, dag.fetchThroughDAGSvc)
			if n := heldBesidesState(t, r, dag.fetchThroughDAGSvc); n != 1 {
				t.Errorf("after a clean compaction the store should hold its snapshot and its state only, holds %d blocks besides its state", n)
			}
			syncReplicaHeads(t, r, control[0])
			assertSameState(t, []string{"crashed", "control"}, r, control[0])
		})
	}
}

func TestRestoredBackupNeverReusesAnIncarnation(t *testing.T) {
	ctx := context.Background()
	live := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	node := aNode()
	open := func(store ds.Datastore, d ipld.DAGService, name string) *Datastore {
		r, err := NewDatastore(store, ds.NewKey("crdttest"), d, &nullBroadcaster{}, onNode(node, replicaOptions(noAutoCompact(), name)))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	a := open(live, dag, "a: ")
	put(t, a, "k", "1")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	backup := dssync.MutexWrap(ds.NewMapDatastore())
	if err := scanStore(ctx, live, ds.NewKey("/"), func(k string, v []byte) error {
		return backup.Put(ctx, ds.RawKey(k), v)
	}); err != nil {
		t.Fatal(err)
	}
	restoredDag := newFetchThrough()
	ch, err := dag.bs.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for k := range ch {
		b, err := dag.bs.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		if err := restoredDag.bs.Put(ctx, b); err != nil {
			t.Fatal(err)
		}
	}

	a = open(live, dag, "a again: ")
	defer a.Close()
	put(t, a, "k", "2")
	put(t, a, "after", "original")

	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	b := replicas[0]
	dagsyncs[0].setRemotes(dag.DAGService)
	syncReplicaHeads(t, a, b)

	restored := open(backup, restoredDag, "restored: ")
	defer restored.Close()
	if restored.replica == a.replica {
		t.Fatal("the restored store drew the incarnation the original is writing with")
	}
	put(t, restored, "k", "3")
	put(t, restored, "after", "restored")

	restoredDag.setRemotes(dag.DAGService, dagsyncs[0].DAGService)
	dag.setRemotes(restoredDag.DAGService)
	dagsyncs[0].setRemotes(dag.DAGService, restoredDag.DAGService)
	for range 2 {
		for _, dst := range []*Datastore{a, b, restored} {
			for _, src := range []*Datastore{a, b, restored} {
				if src != dst {
					syncReplicaHeads(t, src, dst)
				}
			}
		}
	}
	assertSameState(t, []string{"original", "peer", "restored"}, a, b, restored)
	if v := valueOf(t, b, ds.NewKey("after")); v != "original" && v != "restored" {
		t.Fatalf("after = %s", v)
	}
}

// aNode is one process of one node, where its stores remember their incarnations;
// a test starts the node again by making another.
func aNode() *incarnationRegistry { return &incarnationRegistry{} }

func onNode(node *incarnationRegistry, o *Options) *Options {
	o.ReplicaID = []byte("node")
	o.incarnations = node
	return o
}

func openStore(t testing.TB, node *incarnationRegistry, store ds.Datastore, dag ipld.DAGService, name string) *Datastore {
	t.Helper()
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(node, replicaOptions(noAutoCompact(), name)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func closeStore(t testing.TB, r *Datastore) {
	t.Helper()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyStore(t testing.TB, store ds.Datastore) ds.Datastore {
	t.Helper()
	ctx := context.Background()
	cp := newSortedStore()
	if err := scanStore(ctx, store, ds.NewKey("/"), func(k string, v []byte) error {
		return cp.Put(ctx, ds.RawKey(k), v)
	}); err != nil {
		t.Fatal(err)
	}
	return cp
}

func copyBlocks(t testing.TB, dag *fetchThroughDAGSvc) *fetchThroughDAGSvc {
	t.Helper()
	ctx := context.Background()
	cp := newFetchThrough()
	ch, err := dag.bs.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for k := range ch {
		b, err := dag.bs.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		if err := cp.bs.Put(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	return cp
}

func deltaOf(t testing.TB, r *Datastore, c cid.Cid) (dot, uint64) {
	t.Helper()
	_, d, err := (&crdtNodeGetter{NodeGetter: r.dagService}).GetDelta(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return dotFromPB(d.GetDot()), d.GetPrev()
}

func assertChained(t testing.TB, name string, r *Datastore, replica string, counter uint64) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if v := r.cc.counter(replica); v != counter {
		t.Errorf("replica %s covers the incarnation up to %d, want %d", name, v, counter)
	}
	if k := r.cc.inc[replica]; k == nil || k.spans[0].lo != 0 {
		t.Errorf("replica %s does not hold the incarnation from its first counter", name)
	}
	for rep, k := range r.cc.inc {
		if len(k.spans) != 1 {
			t.Errorf("replica %s holds dots of %x past a gap: %v", name, rep, k.spans)
		}
	}
}

func TestReopenContinuesTheIncarnation(t *testing.T) {
	node := aNode()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	peer := replicas[0]
	dagsyncs[0].setRemotes(dag.DAGService)

	a := openStore(t, node, store, dag, "a: ")
	put(t, a, "k", "1")
	put(t, a, "k", "2")
	compact(t, a)
	replica, last := a.replica, a.counter
	syncReplicaHeads(t, a, peer)
	closeStore(t, a)

	a = openStore(t, node, store, dag, "a reopened: ")
	defer a.Close()
	if a.replica != replica {
		t.Fatal("the store drew a new incarnation on a reopen in the process that closed it")
	}
	d, prev := deltaOf(t, a, put(t, a, "k", "3"))
	if d.replica != replica || d.counter != last+1 || prev != last {
		t.Fatalf("the first delta after the reopen is %s with prev %d, want counter %d with prev %d", d, prev, last+1, last)
	}
	syncReplicaHeads(t, a, peer)
	want(t, "peer", peer, map[string]string{"k": "3"})
	for name, r := range map[string]*Datastore{"a": a, "peer": peer} {
		assertChained(t, name, r, replica, last+1)
		if n := len(r.cc.inc); n != 1 {
			t.Errorf("replica %s holds %d vector entries for one incarnation", name, n)
		}
	}
	assertSameState(t, []string{"a", "peer"}, a, peer)
}

func TestANewProcessDrawsANewIncarnation(t *testing.T) {
	node := aNode()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()

	a := openStore(t, node, store, dag, "a: ")
	put(t, a, "k", "1")
	first := a.replica
	closeStore(t, a)

	node = aNode()
	a = openStore(t, node, store, dag, "a in a new process: ")
	defer a.Close()
	if a.replica == first {
		t.Fatal("a new process continued an incarnation it never drew")
	}
	d, prev := deltaOf(t, a, put(t, a, "k", "2"))
	if d.replica != a.replica || d.counter != 1 || prev != 0 {
		t.Fatalf("the first delta of a new incarnation is %s with prev %d", d, prev)
	}
	if n := len(a.cc.inc); n != 2 {
		t.Fatalf("two incarnations wrote, the vector holds %d", n)
	}
}

func TestAnEarlierCopyNeverContinuesTheIncarnation(t *testing.T) {
	node := aNode()
	live := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()

	a := openStore(t, node, live, dag, "a: ")
	put(t, a, "k", "1")
	closeStore(t, a)
	backup, backupDag := copyStore(t, live), copyBlocks(t, dag)

	a = openStore(t, node, live, dag, "a again: ")
	replica := a.replica
	put(t, a, "k", "2")
	put(t, a, "after", "original")
	closeStore(t, a)

	restored := openStore(t, node, backup, backupDag, "restored: ")
	defer restored.Close()
	if restored.replica == replica {
		t.Fatal("a copy taken before the incarnation's last writes continued it")
	}
	put(t, restored, "k", "3")
	assertChained(t, "restored", restored, restored.replica, restored.counter)

	a = openStore(t, node, live, dag, "a reopened: ")
	defer a.Close()
	if a.replica != replica {
		t.Fatal("the original lost its incarnation to a copy that did not take it")
	}
	backupDag.setRemotes(dag.DAGService)
	dag.setRemotes(backupDag.DAGService)
	syncReplicaHeads(t, a, restored)
	syncReplicaHeads(t, restored, a)
	assertSameState(t, []string{"original", "restored"}, a, restored)
	if v := valueOf(t, a, ds.NewKey("after")); v != "original" {
		t.Fatalf("after = %s", v)
	}
}

func TestConcurrentOpensNeverShareAnIncarnation(t *testing.T) {
	node := aNode()
	live := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	a := openStore(t, node, live, dag, "a: ")
	put(t, a, "k", "1")
	replica := a.replica
	closeStore(t, a)

	const n = 8
	copies := make([]ds.Datastore, n)
	for i := range copies {
		copies[i] = copyStore(t, live)
	}
	opened := make([]*Datastore, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := NewDatastore(copies[i], ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(node, replicaOptions(noAutoCompact(), fmt.Sprintf("copy %d: ", i))))
			if err != nil {
				t.Error(err)
				return
			}
			opened[i] = r
		}()
	}
	close(start)
	wg.Wait()

	continued := 0
	seen := map[string]bool{}
	for _, r := range opened {
		if r == nil {
			t.FailNow()
		}
		defer r.Close()
		if r.replica == replica {
			continued++
		}
		if seen[r.replica] {
			t.Errorf("two open stores write under one incarnation")
		}
		seen[r.replica] = true
	}
	if continued != 1 {
		t.Fatalf("%d of %d copies continued the incarnation, want 1", continued, n)
	}
}

func TestAClosedStoreWritesNothing(t *testing.T) {
	node := aNode()
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	a := openStore(t, node, store, dag, "a: ")
	put(t, a, "k", "1")
	closeStore(t, a)

	if err := a.Put(ctx, ds.NewKey("k"), []byte("2")); !errors.Is(err, ErrClosed) {
		t.Fatalf("a put on a closed store returned %v", err)
	}
	if _, err := a.Compact(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("a compaction of a closed store returned %v", err)
	}

	again := openStore(t, node, store, dag, "a reopened: ")
	defer again.Close()
	if again.replica != a.replica {
		t.Fatal("the reopen should continue the incarnation")
	}
	closeStore(t, a)
	twin := openStore(t, node, copyStore(t, store), dag, "twin: ")
	defer twin.Close()
	if twin.replica == again.replica {
		t.Fatal("a second Close handed out the incarnation an open store is writing under")
	}
}
