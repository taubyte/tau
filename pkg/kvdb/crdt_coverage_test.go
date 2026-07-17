package kvdb

// Tests targeting the remaining low-coverage spots in crdt.go called out by
// Item H: Options.verify()'s error/panic branches, DotDAG (0% baseline,
// including its "purged/unavailable" tolerance added for compacted DAGs),
// and InternalStats.

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// TestOptionsVerifyNil checks the opts == nil branch of Options.verify().
func TestOptionsVerifyNil(t *testing.T) {
	var o *Options
	if err := o.verify(); err == nil {
		t.Fatal("expected an error verifying nil Options")
	}
}

// TestOptionsVerifyErrorBranches is a table test hitting every plain error
// branch (as opposed to the two panic branches, tested separately) of
// Options.verify(), each by taking otherwise-valid DefaultOptions() and
// invalidating exactly one field.
func TestOptionsVerifyErrorBranches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(o *Options)
		wantErr string
	}{
		{"RebroadcastInterval zero", func(o *Options) { o.RebroadcastInterval = 0 }, "invalid RebroadcastInterval"},
		{"RebroadcastInterval negative", func(o *Options) { o.RebroadcastInterval = -1 }, "invalid RebroadcastInterval"},
		{"Logger nil", func(o *Options) { o.Logger = nil }, "Logger is undefined"},
		{"NumWorkers zero", func(o *Options) { o.NumWorkers = 0 }, "bad number of NumWorkers"},
		{"NumWorkers negative", func(o *Options) { o.NumWorkers = -1 }, "bad number of NumWorkers"},
		{"DAGSyncerTimeout negative", func(o *Options) { o.DAGSyncerTimeout = -1 }, "invalid DAGSyncerTimeout"},
		{"MaxBatchDeltaSize zero", func(o *Options) { o.MaxBatchDeltaSize = 0 }, "invalid MaxBatchDeltaSize"},
		{"MaxBatchDeltaSize negative", func(o *Options) { o.MaxBatchDeltaSize = -1 }, "invalid MaxBatchDeltaSize"},
		{"RepairInterval negative", func(o *Options) { o.RepairInterval = -1 }, "invalid RepairInterval"},
		{"BroadcastBatchDelay negative", func(o *Options) { o.BroadcastBatchDelay = -1 }, "invalid BroadcastBatchDelay"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := DefaultOptions()
			tc.mutate(o)
			err := o.verify()
			if err == nil {
				t.Fatalf("expected an error for case %q", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error to contain %q, got %q", tc.wantErr, err.Error())
			}
		})
	}

	// DAGSyncerTimeout == 0 is explicitly allowed (means "disabled").
	o := DefaultOptions()
	o.DAGSyncerTimeout = 0
	if err := o.verify(); err != nil {
		t.Fatalf("DAGSyncerTimeout == 0 should be valid (disables the timeout), got %v", err)
	}

	// RepairInterval == 0 and BroadcastBatchDelay == 0 are also explicitly
	// allowed (disabled).
	o2 := DefaultOptions()
	o2.RepairInterval = 0
	o2.BroadcastBatchDelay = 0
	if err := o2.verify(); err != nil {
		t.Fatalf("RepairInterval == 0 and BroadcastBatchDelay == 0 should be valid, got %v", err)
	}
}

// TestOptionsVerifyPanics checks the two "should never happen" invariant
// panics: a nil DeltaFactory, and any empty InternalNamespaces field.
func TestOptionsVerifyPanics(t *testing.T) {
	t.Run("nil DeltaFactory panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected a panic for a nil DeltaFactory")
			}
		}()
		o := DefaultOptions()
		o.crdtOpts.DeltaFactory = nil
		//nolint:errcheck
		o.verify()
	})

	namespaceFields := []struct {
		name   string
		mutate func(o *Options)
	}{
		{"Heads", func(o *Options) { o.crdtOpts.Namespaces.Heads = "" }},
		{"Set", func(o *Options) { o.crdtOpts.Namespaces.Set = "" }},
		{"ProcessedBlocks", func(o *Options) { o.crdtOpts.Namespaces.ProcessedBlocks = "" }},
		{"DirtyBitKey", func(o *Options) { o.crdtOpts.Namespaces.DirtyBitKey = "" }},
		{"BadShutdownKey", func(o *Options) { o.crdtOpts.Namespaces.BadShutdownKey = "" }},
		{"VersionKey", func(o *Options) { o.crdtOpts.Namespaces.VersionKey = "" }},
	}
	for _, tc := range namespaceFields {
		t.Run("empty Namespaces."+tc.name+" panics", func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("expected a panic for empty Namespaces.%s", tc.name)
				}
			}()
			o := DefaultOptions()
			tc.mutate(o)
			//nolint:errcheck
			o.verify()
		})
	}
}

// TestOptionsVerifyValid checks the success path explicitly (DefaultOptions
// as-is passes).
func TestOptionsVerifyValid(t *testing.T) {
	if err := DefaultOptions().verify(); err != nil {
		t.Fatalf("expected DefaultOptions() to be valid, got %v", err)
	}
}

// TestDotDAG checks DotDAG's happy path (0% baseline coverage) and, by
// running it again after Compact, its "purged/unavailable" tolerance branch
// (G4/dotDAGRec): the snapshot head's links point at now-purged blocks that
// DotDAG must not fail on.
func TestDotDAG(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	for i := range 5 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("dot-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Delete(ctx, ds.NewKey("dot-key-0")); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := r.DotDAG(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "digraph CRDTDAG") {
		t.Errorf("expected DotDAG output to start a digraph, got: %s", out)
	}
	if !strings.Contains(out, "subgraph heads") {
		t.Errorf("expected DotDAG output to contain the heads subgraph, got: %s", out)
	}

	// Now compact: the resulting snapshot head's links point at CIDs that
	// have been purged from the DAG service. DotDAG must tolerate this
	// (and so must PrintDAG) rather than erroring out.
	if _, err := r.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	var buf2 bytes.Buffer
	if err := r.DotDAG(ctx, &buf2); err != nil {
		t.Fatalf("DotDAG over a compacted store should tolerate purged links, got error: %v", err)
	}
	out2 := buf2.String()
	if !strings.Contains(out2, "purged/unavailable") {
		t.Errorf("expected DotDAG output over a compacted store to mark purged links, got: %s", out2)
	}

	if err := r.PrintDAG(ctx); err != nil {
		t.Fatalf("PrintDAG over a compacted store should tolerate purged links, got error: %v", err)
	}
}

// TestDeleteNonExistentKey checks Delete's early-return branch: removing a
// key that was never added produces zero tombstones, so Delete must return
// nil without ever publishing a new block (no heads created).
func TestDeleteNonExistentKey(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	headsBefore, _, err := r.heads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Delete(ctx, ds.NewKey("never-existed")); err != nil {
		t.Fatal(err)
	}

	headsAfter, _, err := r.heads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(headsAfter) != len(headsBefore) {
		t.Fatalf("expected no new heads deleting a non-existent key: before=%d after=%d", len(headsBefore), len(headsAfter))
	}

	has, err := r.Has(ctx, ds.NewKey("never-existed"))
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Fatal("expected Has() to report false for a never-added key")
	}
}

// TestInternalStats checks that InternalStats reports the current heads and
// max height consistently with heads.List, and a sane (non-negative)
// QueuedJobs count.
func TestInternalStats(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	// Before any writes: no heads, height 0.
	stats := r.InternalStats(ctx)
	if len(stats.Heads) != 0 {
		t.Errorf("expected no heads before any writes, got %d", len(stats.Heads))
	}
	if stats.MaxHeight != 0 {
		t.Errorf("expected MaxHeight 0 before any writes, got %d", stats.MaxHeight)
	}
	if stats.QueuedJobs < 0 {
		t.Errorf("expected non-negative QueuedJobs, got %d", stats.QueuedJobs)
	}

	for i := range 5 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("stats-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	stats = r.InternalStats(ctx)
	wantHeads, wantHeight, err := r.heads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Heads) != len(wantHeads) {
		t.Errorf("expected %d heads, got %d", len(wantHeads), len(stats.Heads))
	}
	if stats.MaxHeight != wantHeight {
		t.Errorf("expected MaxHeight %d, got %d", wantHeight, stats.MaxHeight)
	}
	if stats.MaxHeight == 0 {
		t.Error("expected a non-zero MaxHeight after writes")
	}
}
