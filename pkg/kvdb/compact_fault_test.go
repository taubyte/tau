package kvdb

// Fault-injection coverage for compact.go's and walkProcessedDAG's error
// branches (Item H) not already reached by compact_test.go's extensive
// behavioral suite.

import (
	"context"
	"errors"
	"sync"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// TestCompactBroadcastError checks Compact's final broadcastHeads error
// branch: the snapshot is built, applied locally, and the old history
// purged, but broadcasting the new snapshot head fails.
func TestCompactBroadcastError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	opts := DefaultOptions()
	wantErr := errors.New("compact broadcast boom")
	bcast := &errBroadcaster{err: wantErr}
	bcast.disabled.Store(true) // let setup writes through; fail only Compact's own broadcast.
	d, err := NewDatastore(fd, ds.NewKey("compactbroadcastfail"), newTestDagsync(), bcast, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	bcast.disabled.Store(false)
	if _, err := d.Compact(ctx, ""); !errors.Is(err, wantErr) {
		t.Fatalf("expected Compact to propagate the broadcastHeads error, got %v", err)
	}

	// The snapshot itself must still have been applied locally despite the
	// broadcast failure (broadcasting happens last).
	if v, err := d.Get(ctx, ds.NewKey("k")); err != nil || string(v) != "v" {
		t.Fatalf("expected the local value to survive despite the broadcast failure: value=%q err=%v", v, err)
	}
}

// TestCompactWalkProcessedDAGError checks Compact's walkProcessedDAG error
// branch (an isProcessed check failing partway through the walk).
func TestCompactWalkProcessedDAGError(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	fd.SetFail(failAlways("Has"))
	if _, err := d.Compact(ctx, ""); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from the walk's isProcessed check, got %v", err)
	}
}

// erroringDAGService wraps an ipld.DAGService and fails Get() for one
// specific, settable CID, to exercise error branches (walkProcessedDAG,
// crdtNodeGetter.GetDelta callers) that depend on a DAG fetch failing for a
// block that is otherwise known-processed/known-good. The target CID is
// mutex-protected (rather than a plain field swapped in on the *Datastore*
// after construction) since a *Datastore built on top of it runs background
// goroutines that may call Get concurrently with a test arming the target.
type erroringDAGService struct {
	ipld.DAGService
	mu      sync.Mutex
	failCid cid.Cid
}

func (e *erroringDAGService) SetFailCid(c cid.Cid) {
	e.mu.Lock()
	e.failCid = c
	e.mu.Unlock()
}

func (e *erroringDAGService) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	e.mu.Lock()
	target := e.failCid
	e.mu.Unlock()
	if c == target {
		return nil, errFault
	}
	return e.DAGService.Get(ctx, c)
}

// TestWalkProcessedDAGFetchError checks walkProcessedDAG's DAG-fetch error
// branch: a block that isProcessed reports as known (so the walk tries to
// fetch and unmarshal it) but whose underlying DAG service fetch fails.
func TestWalkProcessedDAGFetchError(t *testing.T) {
	ctx := context.Background()
	// Built with the erroring DAG service from construction time (no
	// target armed yet, so Put below succeeds normally), rather than
	// swapping store.dagService in after the fact, which would race with
	// NewDatastore()'s background goroutines that read it concurrently.
	dagsvc := &erroringDAGService{DAGService: newTestDagsync()}
	opts := DefaultOptions()
	d, err := NewDatastore(newFaultyDatastore(ds.NewMapDatastore()), ds.NewKey("walkfetchfail"), dagsvc, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

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

	dagsvc.SetFailCid(heads[0].Cid)

	if _, err := d.Compact(ctx, ""); !errors.Is(err, errFault) {
		t.Fatalf("expected Compact's walkProcessedDAG to propagate the DAG fetch failure, got %v", err)
	}
}
