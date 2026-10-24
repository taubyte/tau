package kvdb

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// assertNoLeak checks that every block r holds is indexed, and that every
// index entry names a block r holds.
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
		_, bd, err := decodeBlock(v)
		if err != nil {
			return err
		}
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
	ch, err := d.bs.AllKeysChan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for c := range ch {
		if !indexed[cid.NewCidV1(cid.DagProtobuf, c.Hash())] {
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
	r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, replicaOptions(noAutoCompact(), name))
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
			r, err := NewDatastore(store, ds.NewKey("crdttest"), dag, &nullBroadcaster{}, replicaOptions(noAutoCompact(), "crash: "))
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
			if n := dag.blockCount(t); n != 1 {
				t.Errorf("after a clean compaction the store should hold its snapshot only, holds %d blocks", n)
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
		r, err := NewDatastore(store, ds.NewKey("crdttest"), d, &nullBroadcaster{}, replicaOptions(noAutoCompact(), name))
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
