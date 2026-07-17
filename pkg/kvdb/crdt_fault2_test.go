package kvdb

// More Item H fault-injection coverage: deltaMerge's GetElements/
// GetTombstones error branches (via a custom Delta implementation, since
// pbDelta's own accessors never fail), decodeBroadcast's malformed-input
// branches, and addDAGNode/putBlock/broadcastHeads.

import (
	"context"
	"errors"
	"testing"

	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
	"google.golang.org/protobuf/proto"
)

// errDelta wraps a pbDelta and can be configured to fail GetElements and/or
// GetTombstones on demand, to reach deltaMerge's (otherwise dead, since
// pbDelta's own accessors never fail) error branches.
type errDelta struct {
	*pbDelta
	failElements   bool
	failTombstones bool
}

func newErrDelta() *errDelta {
	return &errDelta{pbDelta: &pbDelta{Delta: &pb.Delta{}}}
}

func (d *errDelta) GetElements() ([]*pb.Element, error) {
	if d.failElements {
		return nil, errFault
	}
	return d.pbDelta.GetElements()
}

func (d *errDelta) GetTombstones() ([]*pb.Element, error) {
	if d.failTombstones {
		return nil, errFault
	}
	return d.pbDelta.GetTombstones()
}

// TestDeltaMergeErrors exercises deltaMerge's four GetElements/GetTombstones
// error branches (d1 and d2, elements and tombstones).
func TestDeltaMergeErrors(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	good := newErrDelta()

	cases := []struct {
		name string
		d1   Delta
		d2   Delta
	}{
		{"d1 elements fail", &errDelta{pbDelta: good.pbDelta, failElements: true}, good},
		{"d2 elements fail", good, &errDelta{pbDelta: good.pbDelta, failElements: true}},
		{"d1 tombstones fail", &errDelta{pbDelta: good.pbDelta, failTombstones: true}, good},
		{"d2 tombstones fail", good, &errDelta{pbDelta: good.pbDelta, failTombstones: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := d.deltaMerge(tc.d1, tc.d2); !errors.Is(err, errFault) {
				t.Fatalf("expected errFault, got %v", err)
			}
		})
	}

	// Sanity: nil d1/d2 (the "use an empty delta" branches) still work.
	merged, err := d.deltaMerge(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if merged.GetPriority() != 0 {
		t.Fatalf("expected priority 0 merging two nils, got %d", merged.GetPriority())
	}
}

// TestDecodeBroadcastErrors checks decodeBroadcast's two error branches:
// malformed protobuf bytes, and a well-formed message whose head Cid bytes
// don't decode as a CID.
func TestDecodeBroadcastErrors(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	ctx := context.Background()

	if _, err := d.decodeBroadcast(ctx, []byte{0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected decodeBroadcast to fail on malformed protobuf bytes")
	}

	bad := &pb.CRDTBroadcast{Heads: []*pb.Head{{Cid: []byte("not-a-cid"), DagName: ""}}}
	badBytes, err := proto.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.decodeBroadcast(ctx, badBytes); err == nil {
		t.Fatal("expected decodeBroadcast to fail on a malformed head Cid")
	}
}

// erroringAddDAGService wraps an ipld.DAGService and fails every Add(), to
// exercise putBlock's dagService.Add error branch.
type erroringAddDAGService struct {
	ipld.DAGService
}

func (e *erroringAddDAGService) Add(ctx context.Context, n ipld.Node) error {
	return errFault
}

// TestAddDAGNodePutBlockError checks addDAGNode's putBlock error branch
// (the underlying DAG service failing to Add the new block).
func TestAddDAGNodePutBlockError(t *testing.T) {
	ctx := context.Background()
	// Built with the erroring DAG service from construction time (rather
	// than swapping store.dagService in after the fact, which would race
	// with NewDatastore()'s background goroutines that read it concurrently):
	// set.Add below never touches the DAG service, so there is no need
	// for any prior successful write through it.
	opts := DefaultOptions()
	d, err := NewDatastore(newFaultyDatastore(ds.NewMapDatastore()), ds.NewKey("adddagnodefail"), &erroringAddDAGService{DAGService: newTestDagsync()}, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	addDelta, err := d.set.Add(ctx, "adddagnode-fail-key", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.addDAGNode(ctx, addDelta); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from putBlock's dagService.Add, got %v", err)
	}
}

// TestBroadcastHeadsEmptyIsNoop checks broadcastHeads' "nothing to
// rebroadcast" short circuit (len(heads) == 0).
func TestBroadcastHeadsEmptyIsNoop(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	if err := d.broadcastHeads(context.Background(), nil); err != nil {
		t.Fatalf("expected a no-op for an empty heads slice, got %v", err)
	}
}

// TestSetMergeGetTombstonesError checks set.Merge's GetTombstones error
// branch (its GetElements error branch is already covered elsewhere via
// optimize_test.go's flakyDelta/TestProcessNodeFailureUnreservesChild).
func TestSetMergeGetTombstonesError(t *testing.T) {
	d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
	bad := &errDelta{pbDelta: &pbDelta{Delta: &pb.Delta{}}, failTombstones: true}
	if err := d.set.Merge(context.Background(), bad, "some-block-id"); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault, got %v", err)
	}
}

// TestUpdateDeltaWithRemoveErrors checks updateDeltaWithRemove's
// GetElements/GetTombstones/deltaMerge error branches, reached through the
// normal crdtBatch.Delete path once a crdtBatch already has a pending curDelta
// whose accessors have been made to fail.
func TestUpdateDeltaWithRemoveErrors(t *testing.T) {
	t.Run("GetElements fails", func(t *testing.T) {
		d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
		d.curDelta = &errDelta{pbDelta: &pbDelta{Delta: &pb.Delta{}}, failElements: true}
		if _, err := d.updateDeltaWithRemove("k", newErrDelta()); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("GetTombstones fails", func(t *testing.T) {
		d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
		d.curDelta = &errDelta{pbDelta: &pbDelta{Delta: &pb.Delta{}}, failTombstones: true}
		if _, err := d.updateDeltaWithRemove("k", newErrDelta()); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})

	t.Run("deltaMerge fails via newDelta", func(t *testing.T) {
		d := newTestDatastore(t, newFaultyDatastore(ds.NewMapDatastore()))
		d.curDelta = &pbDelta{Delta: &pb.Delta{Elements: []*pb.Element{{Key: "other"}}}}
		bad := &errDelta{pbDelta: &pbDelta{Delta: &pb.Delta{}}, failElements: true}
		if _, err := d.updateDeltaWithRemove("k", bad); !errors.Is(err, errFault) {
			t.Fatalf("expected errFault, got %v", err)
		}
	})
}
