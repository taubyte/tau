package kvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
)

// holdRemove is a DAG service whose next RemoveMany, once armed, waits to be
// released, before or after it removes its blocks: a purge caught part way,
// the way a slow disk catches it.
type holdRemove struct {
	*fetchThroughDAGSvc
	armed    atomic.Bool
	before   bool
	removing chan struct{}
	release  chan struct{}
}

func newHoldRemove(before bool) *holdRemove {
	return &holdRemove{fetchThroughDAGSvc: newFetchThrough(), before: before, removing: make(chan struct{}), release: make(chan struct{})}
}

func (h *holdRemove) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	held := h.armed.CompareAndSwap(true, false)
	if held && h.before {
		close(h.removing)
		<-h.release
	}
	err := h.fetchThroughDAGSvc.RemoveMany(ctx, cids)
	if held && !h.before {
		close(h.removing)
		<-h.release
	}
	return err
}

type compacted struct {
	n   int
	err error
}

// compactHeld starts a compaction of r and returns once its purge is held in
// RemoveMany.
func (h *holdRemove) compactHeld(r *Datastore) <-chan compacted {
	h.armed.Store(true)
	done := make(chan compacted, 1)
	go func() {
		n, err := r.Compact(context.Background())
		done <- compacted{n, err}
	}()
	<-h.removing
	return done
}

// lockFree reports, once every other goroutine of the bubble is blocked,
// whether store.mu is free. On a store that holds it while its purge is
// held, it fails the test, after letting the purge finish and closing r.
func lockFree(t *testing.T, r *Datastore, h *holdRemove, done <-chan compacted) {
	t.Helper()
	synctest.Wait()
	if !r.mu.TryLock() {
		close(h.release)
		<-done
		r.Close()
		t.Fatal("the purge holds store.mu while it removes blocks: every walk and the broadcast reader wait for it")
	}
	r.mu.Unlock()
}

func purgingReplica(t *testing.T, store ds.Datastore, dag *holdRemove) *Datastore {
	t.Helper()
	o := replicaOptions(noAutoCompact(), "purging: ")
	o.manual = true
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func indexed(t testing.TB, r *Datastore) int {
	t.Helper()
	n, err := countPrefix(context.Background(), r.store, r.blocksPrefix())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func headsHeld(t testing.TB, r *Datastore, d *fetchThroughDAGSvc) {
	t.Helper()
	hs, _ := r.heads.List()
	for _, h := range hs {
		if !d.holds(h.Cid) {
			t.Errorf("head %s is a block this replica no longer holds", h.Cid)
		}
	}
}

// TestAPurgeLetsWalksAndWritesRunWhileItDrops: a compaction held store.mu
// for the whole of its purge, removing blocks one at a time under it. Under
// load a hold reached 7.9s, and for all of it the broadcast reader and every
// walk waited: sends to the replica backed up and fetches stalled. The purge
// now removes them with the lock released. Meanwhile a walk merges, a write
// lands, and a second compaction finds nothing to fold in what is being
// dropped.
func TestAPurgeLetsWalksAndWritesRunWhileItDrops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		dag := newHoldRemove(false)
		r := purgingReplica(t, newSortedStore(), dag)
		defer r.Close()
		ps, pdags, closeP := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		defer closeP()
		dag.setRemotes(pdags[0].DAGService)
		for i := range 8 {
			put(t, r, fmt.Sprintf("/k%d", i), "v")
		}
		put(t, ps[0], "/p", "x")

		done := dag.compactHeld(r)
		lockFree(t, r, dag, done)
		snap := onlyHead(t, r)
		if n, err := r.Compact(ctx); err != nil || n != 0 {
			t.Errorf("a compaction while the purge drops: dropped %d, %v; want nothing to fold", n, err)
		}
		if onlyHead(t, r) != snap {
			t.Error("a compaction while the purge drops wrote a snapshot of what it is dropping")
		}
		syncReplicaHeads(t, ps[0], r)
		put(t, r, "/mid", "w")
		close(dag.release)
		if c := <-done; c.err != nil || c.n != 8 {
			t.Fatalf("the purge dropped %d, %v; want the 8 blocks the snapshot covers", c.n, c.err)
		}

		want(t, "r", r, map[string]string{"/k0": "v", "/k7": "v", "/p": "x", "/mid": "w"})
		assertNoLeak(t, r, dag.fetchThroughDAGSvc)
		headsHeld(t, r, dag.fetchThroughDAGSvc)
		if n := indexed(t, r); r.blockCount != n {
			t.Errorf("the store counts %d blocks and indexes %d", r.blockCount, n)
		}
	})
}

// TestABlockFetchedAgainWhileItIsDroppedIsNotLeaked: a walk that saw a block
// uncovered before the snapshot covered it fetches it again, from a peer,
// after the purge removed it and before the purge unindexed it. Its merge
// finds the block covered and drops what it fetched: a block the purge has
// taken is not held, though its entry is still there.
func TestABlockFetchedAgainWhileItIsDroppedIsNotLeaked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		dag := newHoldRemove(false)
		r := purgingReplica(t, newSortedStore(), dag)
		defer r.Close()
		ps, pdags, closeP := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		defer closeP()
		pdags[0].setRemotes(dag.DAGService)
		dag.setRemotes(pdags[0].DAGService)
		first := put(t, r, "/a", "1")
		put(t, r, "/b", "2")
		syncReplicaHeads(t, r, ps[0])

		done := dag.compactHeld(r)
		lockFree(t, r, dag, done)
		ng := &crdtNodeGetter{NodeGetter: r.dagService}
		nd, delta, err := ng.GetDelta(ctx, first)
		if err != nil {
			t.Fatal(err)
		}
		if !dag.holds(first) {
			t.Fatal("setup: the fetch did not keep a copy of the block")
		}
		if _, err := r.processNode(ctx, r.currentGeneration(), ng, delta, nd); err != nil {
			t.Fatal(err)
		}
		close(dag.release)
		if c := <-done; c.err != nil {
			t.Fatal(c.err)
		}

		if dag.holds(first) {
			t.Error("the copy fetched while the purge dropped the block is held, and nothing indexes it")
		}
		assertNoLeak(t, r, dag.fetchThroughDAGSvc)
		want(t, "r", r, map[string]string{"/a": "1", "/b": "2"})
	})
}

// TestABlockMergedAgainWhileItIsDroppedWaitsForThePurge: a rebase drops all
// the replica holds while a purge is removing blocks, and a walk of a peer's
// heads reaches one of them again. Merged then, it would be indexed and made
// a head while the purge removes its copy and its entry. It waits instead:
// the walk records it, and the repair after the purge merges it, held.
func TestABlockMergedAgainWhileItIsDroppedWaitsForThePurge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		dag := newHoldRemove(false)
		r := purgingReplica(t, newSortedStore(), dag)
		defer r.Close()
		ps, pdags, closeP := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		defer closeP()
		dag.setRemotes(pdags[0].DAGService)
		put(t, ps[0], "/p1", "1")
		peerHead := put(t, ps[0], "/p2", "2")
		syncReplicaHeads(t, ps[0], r)
		put(t, r, "/own", "v")

		done := dag.compactHeld(r)
		lockFree(t, r, dag, done)
		r.mu.Lock()
		err := r.dropAllButOwn(ctx, r.clock())
		r.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		syncReplicaHeads(t, ps[0], r)
		if r.covered(dotOf(t, ps[0], peerHead)) {
			t.Error("a block the purge is dropping was merged before the purge ended")
		}
		close(dag.release)
		if c := <-done; c.err != nil {
			t.Fatal(c.err)
		}
		if err := r.repairDAG(ctx); err != nil {
			t.Fatal(err)
		}

		want(t, "r", r, map[string]string{"/p1": "1", "/p2": "2"})
		headsHeld(t, r, dag.fetchThroughDAGSvc)
		assertNoLeak(t, r, dag.fetchThroughDAGSvc)
	})
}

func dotOf(t testing.TB, r *Datastore, c cid.Cid) dot {
	t.Helper()
	d, _ := deltaOf(t, r, c)
	return d
}

// TestACrashWhileAPurgeDropsLeaksNothing: the process dies while a purge,
// its lock released, removes blocks, before it removes any and after it
// removed them all, with a write made meanwhile. Reopened, the store holds
// no block it does not index and loses nothing; its next compaction drops
// every entry the crash left naming a block that is gone.
func TestACrashWhileAPurgeDropsLeaksNothing(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(fmt.Sprintf("before the removal %t", before), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				store := newSortedStore()
				dag := newHoldRemove(before)
				r := purgingReplica(t, store, dag)
				for i := range 6 {
					put(t, r, fmt.Sprintf("/k%d", i%4), fmt.Sprintf("v%d", i))
				}
				del(t, r, "/k1")

				done := dag.compactHeld(r)
				lockFree(t, r, dag, done)
				put(t, r, "/mid", "w")
				crashed, crashedDag := copyStore(t, store), copyBlocks(t, dag.fetchThroughDAGSvc)
				close(dag.release)
				if c := <-done; c.err != nil {
					t.Fatal(c.err)
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}

				if err := crashed.Put(ctx, ds.NewKey("crdttest").ChildString(badShutdownKey), nil); err != nil {
					t.Fatal(err)
				}
				o := replicaOptions(noAutoCompact(), "reopened: ")
				o.manual = true
				c, err := NewDatastore(crashed, ds.NewKey("crdttest"), crashedDag, &nullBroadcaster{}, o)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				kv := map[string]string{"/k0": "v4", "/k1": "<deleted>", "/k2": "v2", "/k3": "v3", "/mid": "w"}
				want(t, "reopened", c, kv)
				ch, err := crashedDag.bs.AllKeysChan(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for k := range ch {
					held, err := c.store.Has(ctx, c.blockKey(k))
					if err == nil && !held {
						held = c.reaches(cid.NewCidV1(cid.Raw, k.Hash()))
					}
					if err != nil || !held {
						t.Errorf("block %s is held and indexed nowhere: a leak", k)
					}
				}

				compact(t, c)
				assertNoLeak(t, c, crashedDag)
				if n := heldBesidesState(t, c, crashedDag); n != 1 {
					t.Errorf("after a clean compaction the store should hold its snapshot and its state only, holds %d blocks besides its state", n)
				}
				want(t, "compacted", c, kv)
			})
		})
	}
}

// TestCloseWaitsForAPurgeToDrop: a store closed while its purge drops blocks
// waits for the purge to remove them and their entries before it returns,
// as it waited on the lock the purge held: nothing is written after Close.
func TestCloseWaitsForAPurgeToDrop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newSortedStore()
		dag := newHoldRemove(false)
		r := purgingReplica(t, store, dag)
		for i := range 5 {
			put(t, r, fmt.Sprintf("/k%d", i), "v")
		}
		done := dag.compactHeld(r)
		lockFree(t, r, dag, done)

		closed := make(chan error, 1)
		go func() { closed <- r.Close() }()
		synctest.Wait()
		select {
		case <-closed:
			t.Error("Close returned while a purge was dropping blocks")
		default:
		}
		close(dag.release)
		if c := <-done; c.err != nil || c.n != 5 {
			t.Fatalf("the purge dropped %d, %v", c.n, c.err)
		}
		if err := <-closed; err != nil {
			t.Fatal(err)
		}

		o := replicaOptions(noAutoCompact(), "reopened: ")
		o.manual = true
		re, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, o)
		if err != nil {
			t.Fatal(err)
		}
		defer re.Close()
		assertNoLeak(t, re, dag.fetchThroughDAGSvc)
	})
}

// TestAPurgeThatFailsCountsWhatItLeaves: a purge whose removal or unindexing
// fails leaves its blocks indexed, counted again, for the next purge to take.
func TestAPurgeThatFailsCountsWhatItLeaves(t *testing.T) {
	t.Run("the removal, after a peer's snapshot", func(t *testing.T) {
		dag := &removeFailing{fetchThroughDAGSvc: newFetchThrough()}
		o := replicaOptions(noAutoCompact(), "failing: ")
		o.manual = true
		r, err := NewDatastore(newSortedStore(), ds.NewKey("crdttest"), dag, &nullBroadcaster{}, o)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		ps, pdags, closeP := makeNReplicasSeparateStores(t, 1, noAutoCompact())
		defer closeP()
		pdags[0].setRemotes(dag.DAGService)
		dag.setRemotes(pdags[0].DAGService)
		for i := range 4 {
			put(t, r, fmt.Sprintf("/k%d", i), "v")
		}
		syncReplicaHeads(t, r, ps[0])
		compact(t, ps[0])

		dag.fail.Store(true)
		syncReplicaHeads(t, ps[0], r)
		dag.fail.Store(false)
		if n := indexed(t, r); n != 5 || r.blockCount != n {
			t.Fatalf("after a failed purge the store counts %d blocks and indexes %d, want its 4 and the snapshot", r.blockCount, n)
		}
		compact(t, r)
		assertNoLeak(t, r, dag.fetchThroughDAGSvc)
	})
	t.Run("the unindexing", func(t *testing.T) {
		store := newFaultyDatastore(newSortedStore())
		dag := newFetchThrough()
		o := replicaOptions(noAutoCompact(), "failing: ")
		o.manual = true
		r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, o)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for i := range 4 {
			put(t, r, fmt.Sprintf("/k%d", i), "v")
		}
		store.SetFail(func(op string, key ds.Key) error {
			if op == "BatchDelete" && strings.Contains(key.String(), "/"+processedBlocksNs+"/") {
				return errFault
			}
			return nil
		})
		if _, err := r.Compact(context.Background()); !errors.Is(err, errFault) {
			t.Fatalf("the unindexing should have failed, got %v", err)
		}
		store.SetFail(nil)
		if n := indexed(t, r); n != 5 || r.blockCount != n {
			t.Fatalf("after a failed purge the store counts %d blocks and indexes %d, want its 4 and the snapshot", r.blockCount, n)
		}
		compact(t, r)
		assertNoLeak(t, r, dag)
	})
}

// TestABlockAPurgeIsDroppingIsNotDueForRefresh: blocks past half their
// retention are folded into a fresh snapshot. One a purge is dropping is not:
// it is gone once the purge ends, and the snapshot covering it is young.
func TestABlockAPurgeIsDroppingIsNotDueForRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		dag := newHoldRemove(false)
		o := replicaOptions(noAutoCompact(), "purging: ")
		o.manual = true
		o.Clock = func() time.Time { return now }
		r, err := NewDatastore(newSortedStore(), ds.NewKey("crdttest"), dag, &nullBroadcaster{}, o)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for i := range 3 {
			put(t, r, fmt.Sprintf("/k%d", i), "v")
		}
		now = now.Add(o.Retention / 2)

		done := dag.compactHeld(r)
		lockFree(t, r, dag, done)
		r.mu.Lock()
		due, err := r.refreshDue(context.Background())
		r.mu.Unlock()
		close(dag.release)
		if c := <-done; c.err != nil || c.n != 3 {
			t.Fatalf("the purge dropped %d, %v", c.n, c.err)
		}
		if err != nil || due {
			t.Errorf("a refresh is due (%t, %v) for blocks a purge is dropping", due, err)
		}
	})
}
