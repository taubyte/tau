package kvdb

// Further Item H coverage tests targeting the remaining low-coverage spots
// found by measuring after the first coverage pass: publish's
// nil/empty-delta short circuit and broadcast-error propagation, the
// offline (nil Broadcaster) branches of broadcast/handleNext, crdtBatch.Delete's
// MaxBatchDeltaSize-triggered implicit commit, applyMigrations' v==1-only
// resume branch, set.withDAGTimeout's "disabled" branch, and
// BasicPubSubBroadcaster.Next's own-context-cancelled branch.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"

	libp2p "github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
)

// errBroadcaster is a Broadcaster whose Broadcast() fails with a fixed
// error, and whose Next() just blocks until ctx is done (like
// nullBroadcaster), for tests that need to observe publish()'s (or
// Compact's) error propagation from broadcast(). By default it fails every
// Broadcast() call; set disabled=true (via the disabled field, safe to
// flip at any time) to let calls through until re-armed.
type errBroadcaster struct {
	err      error
	disabled atomic.Bool
}

func (b *errBroadcaster) Broadcast(context.Context, []byte) error {
	if b.disabled.Load() {
		return nil
	}
	return b.err
}
func (b *errBroadcaster) Next(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func newTestDagsync() *mockDAGSvc {
	bs := mdutils.Bserv()
	return &mockDAGSvc{DAGService: merkledag.NewDAGService(bs), bs: bs.Blockstore()}
}

// TestPublishBroadcastError checks that publish() (and therefore Put/Delete)
// propagates a Broadcast() failure rather than silently succeeding once the
// local DAG write/merge has already gone through.
func TestPublishBroadcastError(t *testing.T) {
	opts := DefaultOptions()
	opts.RebroadcastInterval = time.Hour // avoid a stray successful rebroadcast racing the test
	wantErr := errors.New("broadcast boom")

	d, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("errbcast"), newTestDagsync(), &errBroadcaster{err: wantErr}, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	err = d.Put(ctx, ds.NewKey("k"), []byte("v"))
	if err == nil {
		t.Fatal("expected Put to fail when the broadcaster fails")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the error to wrap %v, got %v", wantErr, err)
	}

	// The local merge still went through even though broadcasting failed
	// (addDAGNode/processNode happen before broadcast()).
	got, err := d.Get(ctx, ds.NewKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Fatalf("expected local value 'v' despite the broadcast failure, got %q", got)
	}
}

// TestOfflineNilBroadcaster checks that a Datastore constructed with a nil
// Broadcaster (broadcast()/handleNext()'s "offline" branches) still works
// fully for local reads/writes and does not panic or block anywhere trying
// to use the (absent) broadcaster.
func TestOfflineNilBroadcaster(t *testing.T) {
	opts := DefaultOptions()
	d, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("offline"), newTestDagsync(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	if err := d.Put(ctx, ds.NewKey("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, err := d.Get(ctx, ds.NewKey("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Fatalf("expected 'v', got %q", got)
	}
	if err := d.Delete(ctx, ds.NewKey("k")); err != nil {
		t.Fatal(err)
	}
	if has, err := d.Has(ctx, ds.NewKey("k")); err != nil || has {
		t.Fatalf("expected key to be gone, has=%v err=%v", has, err)
	}
}

// TestCompactOfflineNilBroadcaster checks that Compact runs to completion on
// an offline datastore (nil Broadcaster): broadcasting the resulting
// snapshot head(s) must degrade to a no-op (like broadcast()/handleNext)
// rather than dereferencing the nil broadcaster. Without broadcastHeads'
// offline guard, Compact would panic here on the final broadcast step.
func TestCompactOfflineNilBroadcaster(t *testing.T) {
	opts := DefaultOptions()
	d, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("offline-compact"), newTestDagsync(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	ctx := context.Background()
	for i := range 5 {
		if err := d.Put(ctx, ds.NewKey(fmt.Sprintf("k%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Delete(ctx, ds.NewKey("k0")); err != nil {
		t.Fatal(err)
	}

	n, err := d.Compact(ctx, "")
	if err != nil {
		t.Fatalf("Compact on an offline datastore should succeed, got %v", err)
	}
	if n == 0 {
		t.Fatal("expected Compact to purge the pre-snapshot history")
	}

	if v, err := d.Get(ctx, ds.NewKey("k1")); err != nil || string(v) != "v" {
		t.Fatalf("expected live value to survive offline compaction: value=%q err=%v", v, err)
	}
	if has, err := d.Has(ctx, ds.NewKey("k0")); err != nil || has {
		t.Fatalf("expected deleted key to stay gone after offline compaction, has=%v err=%v", has, err)
	}
}

// TestMerkleCRDTPublishNilOrEmptyDelta checks publish()'s early-return
// branch: a nil Delta, or one with zero elements/tombstones (Size() == 0),
// produces a zero Head and no error, without creating any new DAG block or
// head.
func TestMerkleCRDTPublishNilOrEmptyDelta(t *testing.T) {
	mcrdt := newTestMerkleCRDT(t, nil, nil)
	ctx := context.Background()

	headsBefore, _, err := mcrdt.Heads().List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	head, err := mcrdt.Publish(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if head.Cid.Defined() {
		t.Fatalf("expected an undefined head publishing a nil delta, got %v", head)
	}

	empty := &pbDelta{Delta: &pb.Delta{}}
	head2, err := mcrdt.Publish(ctx, empty)
	if err != nil {
		t.Fatal(err)
	}
	if head2.Cid.Defined() {
		t.Fatalf("expected an undefined head publishing an empty delta, got %v", head2)
	}

	headsAfter, _, err := mcrdt.Heads().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(headsAfter) != len(headsBefore) {
		t.Fatalf("expected no new heads from nil/empty Publish calls: before=%d after=%d", len(headsBefore), len(headsAfter))
	}
}

// TestBatchDeleteTriggersImplicitCommit mirrors TestCRDTBatch's Put-side
// check but for crdtBatch.Delete: enough tombstones accumulated in one crdtBatch
// must cross MaxBatchDeltaSize and trigger an implicit Commit before the
// caller ever calls Commit() itself.
func TestBatchDeleteTriggersImplicitCommit(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()
	opts.MaxBatchDeltaSize = 100 // bytes: small enough that 1-2 tombstones cross it.
	replicas, closeReplicas := makeReplicas(t, opts)
	defer closeReplicas()
	r := replicas[0]

	const numKeys = 10
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("batchdel-%d", i))
		if err := r.Put(ctx, keys[i], []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	btch, err := r.Batch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if err := btch.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
	}

	// Before calling Commit() explicitly, at least the earliest deletes
	// must already have taken effect locally: MaxBatchDeltaSize=100 bytes
	// cannot hold all 10 tombstones in one uncommitted delta.
	anyGoneAlready := false
	for _, k := range keys {
		if has, err := r.Has(ctx, k); err != nil {
			t.Fatal(err)
		} else if !has {
			anyGoneAlready = true
			break
		}
	}
	if !anyGoneAlready {
		t.Fatal("expected crdtBatch.Delete to have implicitly committed at least once before the explicit Commit()")
	}

	if err := btch.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	for _, k := range keys {
		if has, err := r.Has(ctx, k); err != nil || has {
			t.Fatalf("expected key %s to be deleted after Commit, has=%v err=%v", k, has, err)
		}
	}
}

// TestApplyMigrationsResumeFromV1 checks applyMigrations' "if v == 1" branch
// in isolation (as opposed to falling through into it from v == 0, which
// every other test's fresh-replica startup already exercises): a store
// sitting at version 1 must run migrate1to2 (and only that) and end at the
// current version.
func TestApplyMigrationsResumeFromV1(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	if err := r.Put(ctx, ds.NewKey("resume-key"), []byte("v")); err != nil {
		t.Fatal(err)
	}

	if err := r.setVersion(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if err := r.applyMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	v, err := r.getVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != version {
		t.Fatalf("expected version %d after resuming from v1, got %d", version, v)
	}
}

// TestApplyMigrationsAlreadyCurrent checks that applyMigrations is a no-op
// (runs neither migration) when the store is already at the current
// version.
func TestApplyMigrationsAlreadyCurrent(t *testing.T) {
	replicas, closeReplicas := makeNReplicas(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	v, err := r.getVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v != version {
		t.Fatalf("expected a fresh replica to already be at version %d, got %d", version, v)
	}

	if err := r.applyMigrations(ctx); err != nil {
		t.Fatal(err)
	}

	v2, err := r.getVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v2 != version {
		t.Fatalf("expected version to remain %d, got %d", version, v2)
	}
}

// TestWithDAGTimeoutDisabled checks set.withDAGTimeout's "disabled" branch
// (DAGSyncerTimeout == 0): the returned context must not carry a deadline,
// unlike the enabled case used by every other test's DAGSyncerTimeout =
// time.Second.
//
// makeNReplicas/makeReplicas unconditionally override DAGSyncerTimeout to
// 1s for every replica they build (so that broken-block tests fail fast
// rather than hanging), so this needs a store built directly through NewDatastore()
// to actually observe DAGSyncerTimeout == 0.
func TestWithDAGTimeoutDisabled(t *testing.T) {
	opts := DefaultOptions()
	opts.DAGSyncerTimeout = 0
	d, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("dagtimeout-disabled"), newTestDagsync(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	cctx, cancel := d.set.withDAGTimeout(context.Background())
	defer cancel()
	if _, ok := cctx.Deadline(); ok {
		t.Fatal("expected no deadline on the context when DAGSyncerTimeout is 0")
	}

	// Sanity check the enabled case too (another store config), so both
	// branches of withDAGTimeout are visibly exercised side by side here.
	opts2 := DefaultOptions()
	opts2.DAGSyncerTimeout = time.Minute
	d2, err := NewDatastore(dssync.MutexWrap(ds.NewMapDatastore()), ds.NewKey("dagtimeout-enabled"), newTestDagsync(), nil, opts2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d2.Close() }()

	cctx2, cancel2 := d2.set.withDAGTimeout(context.Background())
	defer cancel2()
	if _, ok := cctx2.Deadline(); !ok {
		t.Fatal("expected a deadline on the context when DAGSyncerTimeout is set")
	}
}

// TestPubSubBroadcasterOwnContextDone checks the branch of Next() that
// fires when the broadcaster's own construction context (as opposed to the
// context passed into Next()) is cancelled -- distinct from the
// already-covered "ctx passed to Next() is cancelled" case.
func TestPubSubBroadcasterOwnContextDone(t *testing.T) {
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = h.Close() }()

	ps, err := pubsub.NewGossipSub(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bc, err := NewBasicPubSubBroadcaster(ctx, ps, "own-ctx-done-topic")
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the broadcaster's own context (not the one we pass to Next).
	cancel()

	if _, err := bc.Next(context.Background()); err != ErrNoMoreBroadcast {
		t.Fatalf("expected ErrNoMoreBroadcast when the broadcaster's own context is done, got %v", err)
	}
}
