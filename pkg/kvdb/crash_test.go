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

// removeFailing is a DAG service whose RemoveMany can be made to fail, the
// way a process that dies before deleting anything leaves its blocks.
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

// assertNoLeak checks that every block r holds is indexed or a state block
// its roots reach, and that every index entry and such block names a block
// r holds.
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
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(replicaOptions(noAutoCompact(), name)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestCrashMidCompaction kills a compaction at each of its two windows, the
// snapshot written but not merged and the snapshot merged but nothing
// purged, and reopens the store: it holds exactly the blocks it indexes, and
// it ends in the same state as a replica that never crashed.
func TestCrashMidCompaction(t *testing.T) {
	for _, window := range []string{"before the merge", "before the purge"} {
		t.Run(window, func(t *testing.T) {
			ctx := context.Background()
			store := newFaultyDatastore(ds.NewMapDatastore())
			dag := &removeFailing{fetchThroughDAGSvc: newFetchThrough()}
			r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(replicaOptions(noAutoCompact(), "crash: ")))
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

// TestRestoredBackupNeverReusesAnIncarnation restores a store from a backup
// taken while it was closed, which is byte-identical to the store at that
// moment, and writes to it: its writes carry a new incarnation, so they can
// never be mistaken for the writes the original made after the backup.
func TestRestoredBackupNeverReusesAnIncarnation(t *testing.T) {
	ctx := context.Background()
	live := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	open := func(store ds.Datastore, d ipld.DAGService, name string) *Datastore {
		r, err := NewDatastore(store, ds.NewKey("crdttest"), d, &nullBroadcaster{}, onNode(replicaOptions(noAutoCompact(), name)))
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

// freshProcess gives the test the empty incarnation registry a new process
// starts with.
func freshProcess(t testing.TB) {
	t.Helper()
	old := incarnations
	incarnations = newIncarnationRegistry()
	t.Cleanup(func() { incarnations = old })
}

// onNode is the options of a store these tests reopen, or copy, on the node
// that wrote it: every open has that node's ReplicaID.
func onNode(o *Options) *Options {
	o.ReplicaID = []byte("node")
	return o
}

func openStore(t testing.TB, store ds.Datastore, dag ipld.DAGService, name string) *Datastore {
	t.Helper()
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(replicaOptions(noAutoCompact(), name)))
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

// copyStore is a byte copy of store, the way a backup or a disk image is.
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

// assertChained checks that r's context holds incarnation's counters up to
// counter as one unbroken chain.
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

// TestReopenContinuesTheIncarnation closes a store and opens it again in the
// same process: it goes on writing under the incarnation it closed, and its
// next delta links the last one it committed, so neither it nor a peer that
// merged its history grows a vector entry or a gap.
func TestReopenContinuesTheIncarnation(t *testing.T) {
	freshProcess(t)
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	replicas, dagsyncs, done := makeNReplicasSeparateStores(t, 1, noAutoCompact())
	defer done()
	peer := replicas[0]
	dagsyncs[0].setRemotes(dag.DAGService)

	a := openStore(t, store, dag, "a: ")
	put(t, a, "k", "1")
	put(t, a, "k", "2")
	compact(t, a)
	replica, last := a.replica, a.counter
	syncReplicaHeads(t, a, peer)
	closeStore(t, a)

	a = openStore(t, store, dag, "a reopened: ")
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

// TestANewProcessDrawsANewIncarnation reopens a store in a new process: the
// incarnation it closed is not known there, so it draws another.
func TestANewProcessDrawsANewIncarnation(t *testing.T) {
	freshProcess(t)
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()

	a := openStore(t, store, dag, "a: ")
	put(t, a, "k", "1")
	first := a.replica
	closeStore(t, a)

	freshProcess(t)
	a = openStore(t, store, dag, "a in a new process: ")
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

// TestAnEarlierCopyNeverContinuesTheIncarnation restores, in the process that
// wrote it, a copy of a store taken before its last writes, once the original
// has closed. The copy does not cover the incarnation's last counter, so it
// draws its own: continuing would chain its writes past the original's, which
// it never merged.
func TestAnEarlierCopyNeverContinuesTheIncarnation(t *testing.T) {
	freshProcess(t)
	live := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()

	a := openStore(t, live, dag, "a: ")
	put(t, a, "k", "1")
	closeStore(t, a)
	backup, backupDag := copyStore(t, live), copyBlocks(t, dag)

	a = openStore(t, live, dag, "a again: ")
	replica := a.replica
	put(t, a, "k", "2")
	put(t, a, "after", "original")
	closeStore(t, a)

	restored := openStore(t, backup, backupDag, "restored: ")
	defer restored.Close()
	if restored.replica == replica {
		t.Fatal("a copy taken before the incarnation's last writes continued it")
	}
	put(t, restored, "k", "3")
	assertChained(t, "restored", restored, restored.replica, restored.counter)

	a = openStore(t, live, dag, "a reopened: ")
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

// TestConcurrentOpensNeverShareAnIncarnation opens identical copies of a
// closed store at once: every copy covers the incarnation it closed, and only
// one may continue it.
func TestConcurrentOpensNeverShareAnIncarnation(t *testing.T) {
	freshProcess(t)
	live := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	a := openStore(t, live, dag, "a: ")
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
			r, err := NewDatastore(copies[i], ds.NewKey("crdttest"), dag, &nullBroadcaster{}, onNode(replicaOptions(noAutoCompact(), fmt.Sprintf("copy %d: ", i))))
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

// TestAClosedStoreWritesNothing: once a store has closed, its incarnation may
// already be another open's. It must issue no dot, and a second Close must not
// hand the incarnation out again.
func TestAClosedStoreWritesNothing(t *testing.T) {
	freshProcess(t)
	ctx := context.Background()
	store := dssync.MutexWrap(ds.NewMapDatastore())
	dag := newFetchThrough()
	a := openStore(t, store, dag, "a: ")
	put(t, a, "k", "1")
	closeStore(t, a)

	if err := a.Put(ctx, ds.NewKey("k"), []byte("2")); !errors.Is(err, ErrClosed) {
		t.Fatalf("a put on a closed store returned %v", err)
	}
	if _, err := a.Compact(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("a compaction of a closed store returned %v", err)
	}

	again := openStore(t, store, dag, "a reopened: ")
	defer again.Close()
	if again.replica != a.replica {
		t.Fatal("the reopen should continue the incarnation")
	}
	closeStore(t, a)
	twin := openStore(t, copyStore(t, store), dag, "twin: ")
	defer twin.Close()
	if twin.replica == again.replica {
		t.Fatal("a second Close handed out the incarnation an open store is writing under")
	}
}
