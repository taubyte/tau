package kvdb

// Fault-injection coverage for set.go's error branches (Item H), plus a
// couple of small pure-function unit tests (decodePriority) that were never
// exercised directly.

import (
	"context"
	"errors"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
)

// TestDecodePriority checks decodePriority (the inverse of encodePriority)
// directly, including its malformed-input error branch.
func TestDecodePriority(t *testing.T) {
	for _, prio := range []uint64{0, 1, 42, 1 << 40} {
		buf := encodePriority(prio)
		got, err := decodePriority(buf)
		if err != nil {
			t.Fatalf("decodePriority(%v) failed: %v", buf, err)
		}
		if got != prio {
			t.Fatalf("round trip mismatch: put %d, got %d", prio, got)
		}
	}

	if _, err := decodePriority(nil); err == nil {
		t.Fatal("expected an error decoding a nil/empty priority")
	}
	if _, err := decodePriority([]byte{}); err == nil {
		t.Fatal("expected an error decoding an empty priority")
	}
}

// newTestDatastore builds a *Datastore directly on top of the given store
// (typically a *faultyDatastore) with an offline (nil) broadcaster, for
// tests that need to reach into store.set's private methods with a
// controllable underlying store.
func newTestDatastore(t *testing.T, store ds.Datastore) *Datastore {
	t.Helper()
	opts := DefaultOptions()
	d, err := NewDatastore(store, ds.NewKey("faulttest"), newTestDagsync(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestGetPriorityError checks getPriority's error branch (a Get failure
// other than ds.ErrNotFound).
func TestGetPriorityError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(failAlways("Get"))
	if _, err := d.set.getPriority(ctx, ds.NewKey("k").String()); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestInTombsKeyIDError checks inTombsKeyID's error branch (Has failure).
func TestInTombsKeyIDError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	fd.SetFail(failAlways("Has"))
	if _, err := d.set.inTombsKeyID(ctx, "somekey", "someid"); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestElementAndInSetErrors check Element's and InSet's error branches (a
// Get/Has failure other than not-found).
func TestElementAndInSetErrors(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(failAlways("Get"))
	if _, err := d.Get(ctx, ds.NewKey("k")); !errors.Is(err, errFault) {
		t.Fatalf("expected Get to propagate errFault, got %v", err)
	}

	fd.SetFail(failAlways("Has"))
	if _, err := d.Has(ctx, ds.NewKey("k")); !errors.Is(err, errFault) {
		t.Fatalf("expected Has to propagate errFault, got %v", err)
	}
}

// TestDeleteRmvQueryError checks Delete's propagation of a Query failure
// from set.Rmv's scan over the key's element markers.
func TestDeleteRmvQueryError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(failAlways("Query"))
	if err := d.Delete(ctx, ds.NewKey("k")); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestPurgeKeyBlocksErrors exercises purgeKeyBlocks' several error branches
// directly: the initial Batch(), the scanning Query(), the per-entry
// Delete(), and the final Commit().
func TestPurgeKeyBlocksErrors(t *testing.T) {
	newSetup := func(t *testing.T) (*Datastore, *faultyDatastore, ds.Key, map[cid.Cid]struct{}) {
		t.Helper()
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("purge-key")
		if err := d.Put(ctx, k, []byte("v")); err != nil {
			t.Fatal(err)
		}
		heads, _, err := d.heads.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		dagCIDSet, _, _, err := d.walkProcessedDAG(ctx, headCIDs(heads))
		if err != nil {
			t.Fatal(err)
		}
		return d, fd, k, dagCIDSet
	}

	t.Run("Batch fails", func(t *testing.T) {
		d, fd, k, dagCIDSet := newSetup(t)
		fd.SetFail(failAlways("Batch"))
		if err := d.set.purgeKeyBlocks(context.Background(), k.String(), dagCIDSet, true, true); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("scanning Query fails", func(t *testing.T) {
		d, fd, k, dagCIDSet := newSetup(t)
		fd.SetFail(failAlways("Query"))
		if err := d.set.purgeKeyBlocks(context.Background(), k.String(), dagCIDSet, true, true); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("per-entry Delete fails", func(t *testing.T) {
		d, fd, k, dagCIDSet := newSetup(t)
		fd.SetFail(failAlways("BatchDelete"))
		if err := d.set.purgeKeyBlocks(context.Background(), k.String(), dagCIDSet, true, true); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("Commit fails", func(t *testing.T) {
		d, fd, k, dagCIDSet := newSetup(t)
		fd.SetFail(failAlways("Commit"))
		if err := d.set.purgeKeyBlocks(context.Background(), k.String(), dagCIDSet, true, true); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("final value Put fails after a surviving element", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("purge-key-2")
		// Two versions: purge only the CID of the second (latest, currently
		// winning) one, leaving the first as the surviving best value. This
		// is a real change (stored v2 -> recomputed v1), so purgeKeyBlocks
		// takes the "bestVal != nil" write path (store.Put(valueK,...))
		// rather than being skipped by the hook-quiet no-op check (Item R8).
		if err := d.Put(ctx, k, []byte("v1")); err != nil {
			t.Fatal(err)
		}
		heads1, _, err := d.heads.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		firstGenSet, _, _, err := d.walkProcessedDAG(ctx, headCIDs(heads1))
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Put(ctx, k, []byte("v2")); err != nil {
			t.Fatal(err)
		}
		heads2, _, err := d.heads.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fullSet, _, _, err := d.walkProcessedDAG(ctx, headCIDs(heads2))
		if err != nil {
			t.Fatal(err)
		}
		secondGenSet := map[cid.Cid]struct{}{}
		for c := range fullSet {
			if _, inFirst := firstGenSet[c]; !inFirst {
				secondGenSet[c] = struct{}{}
			}
		}
		if len(secondGenSet) != 1 {
			t.Fatalf("expected exactly one block introduced by the second Put, got %d", len(secondGenSet))
		}

		fd.SetFail(func(op string, key ds.Key) error {
			if op == "Put" && key.String() == d.set.valueKey(k.String()).String() {
				return errFault
			}
			return nil
		})
		if err := d.set.purgeKeyBlocks(ctx, k.String(), secondGenSet, true, false); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault from the surviving-value Put, got %v", err)
		}
	})

	t.Run("no-survivor value/priority Delete fails", func(t *testing.T) {
		// Unlike the earlier "per-entry Delete fails" subtest (which
		// fails the *scanning* deletes issued through the crdtBatch, i.e.
		// "BatchDelete"), this exercises the final cleanup once no
		// element survives the purge: those Delete calls go directly
		// through s.store (not the crdtBatch), i.e. plain "Delete".
		d, fd, k, dagCIDSet := newSetup(t)
		fd.SetFail(failAlways("Delete"))
		err := d.set.purgeKeyBlocks(context.Background(), k.String(), dagCIDSet, true, true)
		if !errors.Is(err, errFault) {
			t.Fatalf("expected errFault from the no-survivor value/priority Delete, got %v", err)
		}
	})
}

// TestSetValueErrors exercises setValue's inTombsKeyID and getPriority
// error branches (via the public Put path is indirect; here we call
// setValue directly for precision) as well as the write-store Put failures
// for the value and the priority.
func TestSetValueErrors(t *testing.T) {
	t.Run("inTombsKeyID fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		fd.SetFail(failAlways("Has"))
		err := d.set.setValue(context.Background(), fd, "k", "id1", []byte("v"), 1)
		if !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("getPriority fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		fd.SetFail(func(op string, key ds.Key) error {
			if op == "Get" {
				return errFault
			}
			return nil
		})
		err := d.set.setValue(context.Background(), fd, "k", "id1", []byte("v"), 1)
		if !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("value Put fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		fd.SetFail(failAlways("Put"))
		err := d.set.setValue(context.Background(), fd, "k", "id1", []byte("v"), 1)
		if !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("priority Put fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		valueK := d.set.valueKey("k")
		fd.SetFail(func(op string, key ds.Key) error {
			// let the value Put through, fail the priority Put.
			if op == "Put" && key.String() != valueK.String() {
				return errFault
			}
			return nil
		})
		err := d.set.setValue(context.Background(), fd, "k", "id1", []byte("v"), 1)
		if !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}

// TestFetchDeltaError checks set.fetchDelta's error branch (a missing/failed
// DAG block).
func TestFetchDeltaError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	unknownCid := randCid(t, "unknown-fetch-delta-block")
	ng := crdtNodeGetter{NodeGetter: d.dagService}
	if _, err := d.set.fetchDelta(ctx, ng, unknownCid); err == nil {
		t.Fatal("expected fetchDelta to fail for an unknown/unfetchable CID")
	}
}

// TestPutElemsPutError checks putElems' write-store Put error branch,
// reached through the normal Put path with a failing underlying store.
func TestPutElemsPutError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" {
			return errFault
		}
		return nil
	})
	if err := d.Put(ctx, ds.NewKey("putelems-fail"), []byte("v")); err == nil {
		t.Fatal("expected Put to fail when the underlying element marker Put fails")
	} else if !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}
