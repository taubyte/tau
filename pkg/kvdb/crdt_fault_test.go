package kvdb

// Fault-injection coverage for crdt.go's NewDatastore()/Close()/MarkDirty/IsDirty/
// MarkClean/processNode error branches (Item H), and merklecrdt.go's
// PurgeDAG error branches.

import (
	"context"
	"errors"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// TestNewBadShutdownKeyErrors exercises NewDatastore()'s three bad-shutdown-key
// branches: the initial Has check, the Put, and the Sync.
func TestNewBadShutdownKeyErrors(t *testing.T) {
	cases := []struct {
		name string
		op   string
	}{
		{"Has fails", "Has"},
		{"Put fails", "Put"},
		{"Sync fails", "Sync"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := newFaultyDatastore(ds.NewMapDatastore())
			fd.SetFail(failAlways(tc.op))
			opts := DefaultOptions()
			_, err := NewDatastore(fd, ds.NewKey("newfail"), newTestDagsync(), nil, opts)
			if !errors.Is(err, errFault) {
				t.Fatalf("expected errFault from NewDatastore() when %s, got %v", tc.name, err)
			}
		})
	}
}

// TestNewApplyMigrationsError checks that NewDatastore() propagates a failure from
// applyMigrations (here, getVersion's underlying Get failing).
func TestNewApplyMigrationsError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	fd.SetFail(failAlways("Get"))
	opts := DefaultOptions()
	_, err := NewDatastore(fd, ds.NewKey("newmigfail"), newTestDagsync(), nil, opts)
	if !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from NewDatastore() propagating an applyMigrations failure, got %v", err)
	}
}

// TestCloseErrors checks Close()'s two (logged-only, non-fatal) error
// branches: failing to delete the bad-shutdown key, and failing to sync
// that deletion. Close() must still return nil in both cases (the errors
// are logged, not propagated) -- that is itself part of the contract being
// tested here.
func TestCloseErrors(t *testing.T) {
	t.Run("Delete fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		fd.SetFail(failAlways("Delete"))
		if err := d.Close(); err != nil {
			t.Fatalf("expected Close() to swallow the Delete error and return nil, got %v", err)
		}
	})

	t.Run("Sync fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		fd.SetFail(failAlways("Sync"))
		if err := d.Close(); err != nil {
			t.Fatalf("expected Close() to swallow the Sync error and return nil, got %v", err)
		}
	})
}

// TestMarkDirtyIsDirtyMarkCleanErrors checks that these three (logged-only)
// helpers tolerate a failing underlying store without panicking, and that
// IsDirty's documented zero-value-on-error behavior holds.
func TestMarkDirtyIsDirtyMarkCleanErrors(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	fd.SetFail(failAlways("Put"))
	d.MarkDirty(ctx) // must not panic; error is logged only.

	fd.SetFail(failAlways("Has"))
	if dirty := d.IsDirty(ctx); dirty {
		t.Fatal("expected IsDirty to report false when the underlying Has fails")
	}

	fd.SetFail(failAlways("Delete"))
	d.MarkClean(ctx) // must not panic; error is logged only.
}

// TestProcessNodeMarkProcessedError checks processNode's markProcessed
// error branch: the merge succeeds, but recording the block as processed
// fails.
func TestProcessNodeMarkProcessedError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	addDelta, err := d.set.Add(ctx, "processnode-fail-key", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}

	node, err := makeNode(addDelta, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := Head{Cid: node.Cid(), HeadValue: HeadValue{Height: 1}}
	ng := &crdtNodeGetter{NodeGetter: d.dagService}

	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" && key.String() == d.processedBlockKey(node.Cid()).String() {
			return errFault
		}
		return nil
	})

	_, err = d.processNode(ctx, ng, root, addDelta, node, false)
	if !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from markProcessed, got %v", err)
	}
}

// TestProcessNodeHeadsAddError checks processNode's "reached the bottom, add
// as head" error branch (heads.Add failing).
func TestProcessNodeHeadsAddError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	addDelta, err := d.set.Add(ctx, "processnode-headsadd-key", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := makeNode(addDelta, nil)
	if err != nil {
		t.Fatal(err)
	}
	root := Head{Cid: node.Cid(), HeadValue: HeadValue{Height: 1}}
	ng := &crdtNodeGetter{NodeGetter: d.dagService}

	// Let every other write through (markProcessed, element/value/priority
	// markers) and fail only the heads write itself, to isolate this
	// specific branch.
	headKey := d.heads.key(root)
	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" && key.String() == headKey.String() {
			return errFault
		}
		return nil
	})

	_, err = d.processNode(ctx, ng, root, addDelta, node, false)
	if !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from heads.Add, got %v", err)
	}
}

// TestPurgeDAGErrors exercises PurgeDAG's Batch()/Delete()/Commit() error
// branches (the walk itself and purgeKeyBlocks are already covered
// elsewhere).
func TestPurgeDAGErrors(t *testing.T) {
	newSetup := func(t *testing.T) (*MerkleCRDT, *faultyDatastore) {
		t.Helper()
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		if err := d.Put(ctx, ds.NewKey("purgedag-key"), []byte("v")); err != nil {
			t.Fatal(err)
		}
		return &MerkleCRDT{Datastore: d}, fd
	}

	t.Run("Batch fails", func(t *testing.T) {
		mcrdt, fd := newSetup(t)
		fd.SetFail(failAlways("Batch"))
		if _, err := mcrdt.PurgeDAG(context.Background(), ""); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("processed-marker Delete fails", func(t *testing.T) {
		mcrdt, fd := newSetup(t)
		fd.SetFail(failAlways("BatchDelete"))
		if _, err := mcrdt.PurgeDAG(context.Background(), ""); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("Commit fails", func(t *testing.T) {
		mcrdt, fd := newSetup(t)
		fd.SetFail(failAlways("Commit"))
		if _, err := mcrdt.PurgeDAG(context.Background(), ""); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("heads.DeleteDAG fails", func(t *testing.T) {
		mcrdt, fd := newSetup(t)
		// Let the Batch/Delete/Commit sequence for processed-block
		// markers succeed, but fail the final heads deletion write.
		heads, _, err := mcrdt.heads.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(heads) == 0 {
			t.Fatal("expected at least one head")
		}
		headKey := mcrdt.heads.key(heads[0])
		fd.SetFail(func(op string, key ds.Key) error {
			if op == "BatchDelete" && key.String() == headKey.String() {
				return errFault
			}
			return nil
		})
		if _, err := mcrdt.PurgeDAG(context.Background(), ""); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}
