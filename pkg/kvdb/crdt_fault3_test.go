package kvdb

// Further Item H fault-injection coverage, from a second pass over the
// coverage report: broadcast's cheap short-circuit branches, rmvToDelta
// (reached via crdtBatch.Delete, distinct from Datastore.Delete's own call to
// set.Rmv), heads.delete's ErrNotFound-tolerance branch, the crdt.go
// getPriority helper's error branches, extractDelta's non-ProtoNode branch,
// and putTombs' several error branches.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dag "github.com/ipfs/boxo/ipld/merkledag"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	query "github.com/ipfs/go-datastore/query"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// TestBroadcastShortCircuits checks broadcast's two cheap early-return
// branches: an undefined head Cid ("nothing to rebroadcast"), and an
// already-cancelled context.
func TestBroadcastShortCircuits(t *testing.T) {
	// Built with a real (always-failing) broadcaster from construction
	// time, rather than swapping store.broadcaster in after the fact: NewDatastore()
	// starts several background goroutines (handleNext among them) that
	// read store.broadcaster immediately, so assigning it post-construction
	// would race with them.
	opts := DefaultOptions()
	d, err := NewDatastore(newFaultyDatastore(ds.NewMapDatastore()), ds.NewKey("broadcastshortcircuit"), newTestDagsync(), &errBroadcaster{err: errFault}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	if err := d.broadcast(context.Background(), Head{}, nil); err != nil {
		t.Fatalf("expected a no-op for an undefined head Cid, got %v", err)
	}

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.broadcast(cancelledCtx, Head{Cid: randCid(t, "some-head")}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from an already-cancelled context, got %v", err)
	}
}

// TestRmvToDeltaQueryError checks rmvToDelta's error branch (distinct from
// Datastore.Delete's own direct call to set.Rmv): reached through
// crdtBatch.Delete.
func TestRmvToDeltaQueryError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	btch, err := d.Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fd.SetFail(failAlways("Query"))
	if err := btch.Delete(ctx, ds.NewKey("k")); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from rmvToDelta's set.Rmv, got %v", err)
	}
}

// TestHeadsDeleteToleratesErrNotFound checks heads.delete's defensive
// ErrNotFound-tolerance branch: some ds.Write implementations may return
// ds.ErrNotFound for deleting a nonexistent key (even though the current
// go-datastore contract does not), and heads.delete must swallow it.
func TestHeadsDeleteToleratesErrNotFound(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	hh := newTestHeadsWithStore(t, fd)

	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Delete" {
			return ds.ErrNotFound
		}
		return nil
	})

	h := Head{Cid: randCid(t, "never-added")}
	if err := hh.delete(context.Background(), fd, h); err != nil {
		t.Fatalf("expected heads.delete to swallow ds.ErrNotFound, got %v", err)
	}
}

// TestCrdtGetPriorityHelperErrors checks the crdt.go-level getPriority
// helper's two error branches (distinct from set.getPriority): a DAG fetch
// failure, and an Unmarshal failure on a block whose data is not a valid
// Delta.
func TestCrdtGetPriorityHelperErrors(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: d.dagService}

	t.Run("GetDelta fails", func(t *testing.T) {
		if _, err := d.getPriority(ctx, ng, randCid(t, "unknown-block")); err == nil {
			t.Fatal("expected an error fetching an unknown block")
		}
	})

	t.Run("Unmarshal fails", func(t *testing.T) {
		// A ProtoNode whose data is not a valid marshaled Delta: a
		// single 0x80 byte is an incomplete/invalid protobuf varint
		// tag, so proto.Unmarshal must fail on it.
		node := dag.NodeWithData([]byte{0x80})
		if err := node.SetCidBuilder(dag.V1CidPrefix()); err != nil {
			t.Fatal(err)
		}
		if err := d.dagService.Add(ctx, node); err != nil {
			t.Fatal(err)
		}
		if _, err := d.getPriority(ctx, ng, node.Cid()); err == nil {
			t.Fatal("expected an error unmarshaling invalid delta bytes")
		}
	})
}

// fakeRawNode is a minimal ipld.Node that is not a *merkledag.ProtoNode, to
// exercise extractDelta's type-assertion error branch.
type fakeRawNode struct {
	ipld.Node
	c cid.Cid
}

func (n *fakeRawNode) Cid() cid.Cid { return n.c }

// TestExtractDeltaNotProtoNode checks extractDelta's "node is not a
// ProtoNode" error branch.
func TestExtractDeltaNotProtoNode(t *testing.T) {
	if _, err := extractDelta(&fakeRawNode{c: randCid(t, "fake")}); err == nil {
		t.Fatal("expected extractDelta to fail for a non-ProtoNode")
	}
}

// TestPutTombsErrors exercises putTombs' several error branches (the
// initial Batch(), the per-tombstone Put(), findBestValue, the no-survivor
// Delete()s, the surviving-value Put()/setPriority, and the final Commit())
// through the normal Delete() API with a failing underlying store.
func TestPutTombsErrors(t *testing.T) {
	t.Run("Batch fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("puttombs-crdtBatch-key")
		if err := d.Put(ctx, k, []byte("v")); err != nil {
			t.Fatal(err)
		}
		delta, err := d.set.Rmv(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		tombs, err := delta.GetTombstones()
		if err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Batch"))
		if err := d.set.putTombs(ctx, tombs, "block-id", false); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("tomb Put fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("puttombs-put-key")
		if err := d.Put(ctx, k, []byte("v")); err != nil {
			t.Fatal(err)
		}
		delta, err := d.set.Rmv(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		tombs, err := delta.GetTombstones()
		if err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("BatchPut"))
		if err := d.set.putTombs(ctx, tombs, "block-id", false); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("no-survivor Delete fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("puttombs-delete-key")
		if err := d.Put(ctx, k, []byte("v")); err != nil {
			t.Fatal(err)
		}
		delta, err := d.set.Rmv(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		tombs, err := delta.GetTombstones()
		if err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("BatchDelete"))
		if err := d.set.putTombs(ctx, tombs, "block-id", false); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("Commit fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("puttombs-commit-key")
		if err := d.Put(ctx, k, []byte("v")); err != nil {
			t.Fatal(err)
		}
		delta, err := d.set.Rmv(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		tombs, err := delta.GetTombstones()
		if err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Commit"))
		if err := d.set.putTombs(ctx, tombs, "block-id", false); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("surviving-value Put fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		k := ds.NewKey("puttombs-survivor-key")
		// Two versions: tombstone only the first, leaving the second
		// as a surviving element so putTombs takes the "v != nil"
		// (Put valueK / setPriority) path.
		if err := d.Put(ctx, k, []byte("v1")); err != nil {
			t.Fatal(err)
		}
		res, err := d.store.Query(ctx, query.Query{Prefix: d.set.elemsPrefix(k.String()).String(), KeysOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		var firstID string
		for e := range res.Next() {
			if e.Error != nil {
				t.Fatal(e.Error)
			}
			firstID = ds.NewKey(e.Key).Name()
		}
		_ = res.Close()
		if firstID == "" {
			t.Fatal("could not find the first element marker")
		}
		if err := d.Put(ctx, k, []byte("v2")); err != nil {
			t.Fatal(err)
		}

		fd.SetFail(func(op string, key ds.Key) error {
			if op == "BatchPut" && key.String() == d.set.valueKey(k.String()).String() {
				return errFault
			}
			return nil
		})
		tombs := []*pb.Element{{Key: k.String(), Id: firstID}}
		if err := d.set.putTombs(ctx, tombs, "block-id", false); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}

// poisonDelta wraps a pbDelta and fails GetElements() once armed (via a
// shared, closure-captured *atomic.Bool), regardless of which delta
// instance is asked -- used to reach dagWorker's own processNode-failure
// branch (as opposed to calling processNode directly, which
// TestProcessNodeMarkProcessedError and friends already do): dagWorker is
// the real async consumer of store.jobQueue, fed via
// handleBranch/sendNewJobs/sendJobWorker, and it swallows the error (log +
// MarkDirty) rather than returning it to the original caller.
type poisonDelta struct {
	*pbDelta
	armed *atomic.Bool
}

func (d *poisonDelta) GetElements() ([]*pb.Element, error) {
	if d.armed.Load() {
		return nil, errFault
	}
	return d.pbDelta.GetElements()
}

// TestDagWorkerProcessNodeError checks dagWorker's processNode-failure
// branch: a block is written and processed normally (poison disarmed), then
// reprocessed via handleBranch (poison armed) -- exactly what repairDAG
// does for a dirty branch -- routing the failure through the real
// jobQueue/dagWorker pipeline instead of a direct processNode call. Since
// dagWorker only logs and marks the store dirty (fire-and-forget), the test
// polls IsDirty rather than expecting an error return.
func TestDagWorkerProcessNodeError(t *testing.T) {
	armed := &atomic.Bool{}
	opts := DefaultOptions()
	opts.crdtOpts.DeltaFactory = func() Delta {
		return &poisonDelta{pbDelta: &pbDelta{Delta: &pb.Delta{}}, armed: armed}
	}

	fd := newFaultyDatastore(ds.NewMapDatastore())
	d, err := NewDatastore(fd, ds.NewKey("dagworkerfail"), newTestDagsync(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	heads, _, err := d.heads.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 1 {
		t.Fatalf("expected 1 head, got %d", len(heads))
	}
	head := heads[0]

	if d.IsDirty(ctx) {
		t.Fatal("expected a clean store before reprocessing")
	}

	armed.Store(true)
	if err := d.handleBranch(ctx, head, head.Cid); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !d.IsDirty(ctx) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for dagWorker's processNode failure to mark the store dirty")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSendNewJobsErrors exercises sendNewJobs' own error/warning branches
// directly: the root.Height==0 special-case getPriority failure, a missing
// child (deltaOpt.err != nil, which also exercises GetDeltas' own error
// branch and the "GetDeltas did not include all children" safeguard for
// the same child), and a child whose block does not unmarshal as a valid
// Delta (warn-and-continue, not a returned error).
func TestSendNewJobsErrors(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: d.dagService}
	missingCid := randCid(t, "sendnewjobs-missing")

	t.Run("root.Height==0 getPriority fails", func(t *testing.T) {
		var session sync.WaitGroup
		err := d.sendNewJobs(ctx, &session, ng, Head{}, []cid.Cid{missingCid})
		session.Wait()
		if err == nil {
			t.Fatal("expected an error from the root-priority special case")
		}
	})

	t.Run("missing child: GetDeltas error + goodDeltas safeguard", func(t *testing.T) {
		var session sync.WaitGroup
		err := d.sendNewJobs(ctx, &session, ng, Head{HeadValue: HeadValue{Height: 1}}, []cid.Cid{missingCid})
		session.Wait()
		if err == nil {
			t.Fatal("expected an error for a missing child block")
		}
		if d.queuedChildren.Has(missingCid) {
			t.Fatal("expected the safeguard to have removed the unfetched child from queuedChildren")
		}
	})

	t.Run("invalid delta bytes: warn and continue", func(t *testing.T) {
		// sendNewJobs logs a warning and `continue`s past an
		// unmarshalable child rather than aborting the loop early
		// (unlike the deltaOpt.err != nil case above, which breaks):
		// other children keep being processed. The Unmarshal error
		// itself is left assigned to the shared `err` return value
		// though, since nothing resets it on the `continue` path, so
		// sendNewJobs still surfaces it to the caller once the loop
		// (here, of a single child) ends.
		node := dag.NodeWithData([]byte{0x80})
		if err := node.SetCidBuilder(dag.V1CidPrefix()); err != nil {
			t.Fatal(err)
		}
		if err := d.dagService.Add(ctx, node); err != nil {
			t.Fatal(err)
		}
		var session sync.WaitGroup
		err := d.sendNewJobs(ctx, &session, ng, Head{HeadValue: HeadValue{Height: 1}}, []cid.Cid{node.Cid()})
		session.Wait()
		if err == nil {
			t.Fatal("expected the Unmarshal error to be surfaced")
		}
	})
}

// TestGetDeltasExtractDeltaError checks GetDeltas' own extractDelta-failure
// branch: a fetchable block that is not a *merkledag.ProtoNode (a
// dag.RawNode here) so extractDelta's type assertion fails inside the
// GetDeltas loop, as opposed to TestExtractDeltaNotProtoNode which calls
// extractDelta directly.
func TestGetDeltasExtractDeltaError(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	ctx := context.Background()

	raw := dag.NewRawNode([]byte("not-a-delta"))
	if err := d.dagService.Add(ctx, raw); err != nil {
		t.Fatal(err)
	}

	ng := &crdtNodeGetter{NodeGetter: d.dagService}
	var gotErr error
	for opt := range ng.GetDeltas(ctx, []cid.Cid{raw.Cid()}) {
		if opt.err != nil {
			gotErr = opt.err
		}
	}
	if gotErr == nil {
		t.Fatal("expected GetDeltas to report an error for a non-ProtoNode block")
	}
}

// TestEncodeBroadcastSkipsUndefCid checks encodeBroadcast's "nothing to
// rebroadcast" per-head skip branch (a Head with an undefined Cid mixed in
// among real ones).
func TestEncodeBroadcastSkipsUndefCid(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	ctx := context.Background()
	real := randCid(t, "encode-broadcast-real")

	raw, err := d.encodeBroadcast(ctx, []Head{{Cid: cid.Undef}, {Cid: real}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := d.decodeBroadcast(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	heads := decoded[""]
	if len(heads) != 1 || heads[0].Cid != real {
		t.Fatalf("expected exactly the one defined head to survive encoding, got %v", heads)
	}
}

// TestCompactSnapshotStateQueryErrors checks compactSnapshotState's two
// initial Query error branches (the elems-namespace scan and the
// tombs-namespace scan), reached through Compact with a failing underlying
// store.
func TestCompactSnapshotStateQueryErrors(t *testing.T) {
	t.Run("elems Query fails", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
			t.Fatal(err)
		}
		fd.SetFail(failAlways("Query"))
		if _, err := d.Compact(ctx, ""); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("tombs Query fails (second call)", func(t *testing.T) {
		fd := newFaultyDatastore(ds.NewMapDatastore())
		d := newTestDatastore(t, fd)
		ctx := context.Background()
		if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
			t.Fatal(err)
		}
		var queryCalls atomic.Int64
		fd.SetFail(func(op string, key ds.Key) error {
			if op != "Query" {
				return nil
			}
			// Let every Query succeed except the (second)
			// tombs-namespace scan inside compactSnapshotState:
			// walkProcessedDAG issues no Queries, so the first Query
			// compactSnapshotState itself issues is elems, the
			// second is tombs.
			n := queryCalls.Add(1)
			if n == 2 {
				return errFault
			}
			return nil
		})
		if _, err := d.Compact(ctx, ""); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}

// TestPurgeDAGWalkProcessedDAGError checks PurgeDAG's own walkProcessedDAG
// error branch (distinct call site from Compact's, which
// TestCompactWalkProcessedDAGError already covers).
func TestPurgeDAGWalkProcessedDAGError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()
	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(failAlways("Has"))
	mcrdt := &MerkleCRDT{Datastore: d}
	if _, err := mcrdt.PurgeDAG(ctx, ""); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from the walk's isProcessed check, got %v", err)
	}
}
