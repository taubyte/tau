package kvdb

// R10/R11: the per-replica-blockstore test harness and the end-to-end
// receiver-side reclamation tests that need it.
//
// reclaim_test.go's tests drive processNode directly on a single
// replica/store to check the reclaim mechanism in isolation. That is not
// enough on its own: the rest of this package's test suite (makeNReplicas,
// makeNReplicasNoBcast) shares ONE blockstore across every replica
// (mdutils.Bserv() called once, handed to every replica's DAGService). That
// is exactly the class of bug reclaim can have: a replica removing blocks
// another replica still needs is impossible to observe over a shared
// blockstore, because purging on one "replica" purges it for all of them --
// which can never happen in production, where every replica has its own
// store. makeNReplicasSeparateStores below gives every replica its own
// blockstore/DAGService, wired together with fetchThroughDAGSvc so that
// syncing still works (a replica that does not have a block fetches it from
// a designated remote replica and caches it locally, mimicking bitswap),
// while still making a replica's own purge visible only to that replica.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/boxo/ipld/merkledag"
	mdutils "github.com/ipfs/boxo/ipld/merkledag/test"
	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	ipld "github.com/ipfs/go-ipld-format"
)

// fetchThroughDAGSvc is an ipld.DAGService backed by its own local
// blockstore-backed DAGService that additionally knows how to fetch (and
// locally cache) a block it does not have from one other "remote"
// DAGService -- mimicking what a real bitswap-backed DAGService does,
// without pulling in a real exchange/network stack.
//
// A block this replica has never fetched, or has since reclaimed/purged, is
// genuinely absent from its OWN local DAGService: Get/GetMany only ever
// return it via the remote fetch-and-cache path, and RemoveMany (used by
// PurgeDAG/Compact/reclaimCovered) only ever touches the local one. That
// combination is exactly the property the shared-blockstore harness cannot
// exercise, and the one receiver-side reclaim most needs tested against.
type fetchThroughDAGSvc struct {
	ipld.DAGService // local, this replica's own blockstore.

	// remote is fetched-through on a local miss; nil for a replica with
	// nothing to fetch from (e.g. the writer/hub replica in every test
	// below, which is always the sync source and never the sink).
	remote ipld.DAGService

	// remoteFetches counts blocks actually pulled from remote and cached
	// locally. Test-observability only; not required for correctness.
	remoteFetches int64
	fetchMu       sync.Mutex
}

func (f *fetchThroughDAGSvc) Get(ctx context.Context, c cid.Cid) (ipld.Node, error) {
	nd, err := f.DAGService.Get(ctx, c)
	if err == nil {
		return nd, nil
	}
	if f.remote == nil {
		return nil, err
	}
	rnd, rerr := f.remote.Get(ctx, c)
	if rerr != nil {
		// Surface the local (not remote) error, as a real DAGService
		// backed by bitswap over a locally-missing block would.
		return nil, err
	}
	if addErr := f.DAGService.Add(ctx, rnd); addErr != nil {
		return nil, addErr
	}
	f.fetchMu.Lock()
	f.remoteFetches++
	f.fetchMu.Unlock()
	return rnd, nil
}

// GetMany fetches each requested CID independently via Get (which already
// implements the local-then-remote-fetch-through logic): the extra
// concurrency a production GetMany would offer is not needed for these
// small, deterministic tests.
func (f *fetchThroughDAGSvc) GetMany(ctx context.Context, cids []cid.Cid) <-chan *ipld.NodeOption {
	out := make(chan *ipld.NodeOption, len(cids))
	go func() {
		defer close(out)
		var wg sync.WaitGroup
		for _, c := range cids {
			wg.Add(1)
			go func(c cid.Cid) {
				defer wg.Done()
				nd, err := f.Get(ctx, c)
				out <- &ipld.NodeOption{Node: nd, Err: err}
			}(c)
		}
		wg.Wait()
	}()
	return out
}

// makeNReplicasSeparateStores is like makeNReplicasNoBcast (broadcasting
// disabled; tests drive syncing deterministically via handleBlock/
// syncReplicaHeads) except every replica gets its own blockstore/DAGService
// (R10) instead of sharing one: replicas[0] is the "hub" (nothing to fetch
// through; every test below uses it as the writer/source of truth) and
// every other replica fetch-through (and locally caches) from replicas[0].
func makeNReplicasSeparateStores(t testing.TB, n int, opts *Options) ([]*Datastore, []*fetchThroughDAGSvc, func()) {
	t.Helper()

	replicaOpts := make([]*Options, n)
	for i := range replicaOpts {
		if opts == nil {
			replicaOpts[i] = DefaultOptions()
		} else {
			cp := *opts
			replicaOpts[i] = &cp
		}
		replicaOpts[i].Logger = &testLogger{
			name: fmt.Sprintf("sr#%d: ", i),
			l:    DefaultOptions().Logger,
		}
		replicaOpts[i].DAGSyncerTimeout = time.Second
	}

	dagsyncs := make([]*fetchThroughDAGSvc, n)
	replicas := make([]*Datastore, n)
	for i := range replicas {
		bs := mdutils.Bserv()
		dagsyncs[i] = &fetchThroughDAGSvc{DAGService: merkledag.NewDAGService(bs)}

		var err error
		replicas[i], err = NewDatastore(
			makeStore(t, i),
			ds.NewKey("crdttest"),
			dagsyncs[i],
			&nullBroadcaster{},
			replicaOpts[i],
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < n; i++ {
		dagsyncs[i].remote = dagsyncs[0].DAGService
	}

	closeReplicas := func() {
		for i, r := range replicas {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
			// nolint:errcheck
			os.RemoveAll(storeFolder(i))
		}
	}
	return replicas, dagsyncs, closeReplicas
}

// snapshotMetaOf fetches h's delta and returns its snapshot generation
// metadata (R1/R2), fatally failing the test if it cannot.
func snapshotMetaOf(t testing.TB, r *Datastore, h Head) (total uint32, id []byte) {
	t.Helper()
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: r.dagService}
	_, deltaBytes, err := ng.GetDelta(ctx, h.Cid)
	if err != nil {
		t.Fatal(err)
	}
	delta := r.newDelta()
	if err := delta.Unmarshal(deltaBytes); err != nil {
		t.Fatal(err)
	}
	return delta.SnapshotMeta()
}

// TestReclaimOnSnapshotReceive is the core R10/R11 end-to-end regression
// test for receiver-side reclamation (R3/R4/R5): using two replicas with
// separate blockstores, a receiver that already synced a writer's full
// history and then merges the writer's later Compact() snapshot must
// auto-reclaim its own local copy of the now-covered history. This is not
// observable at all over the shared-blockstore harness (the writer's purge
// would silently also "purge" the receiver's blocks, since they are the
// same underlying blocks) -- exactly why R10 exists.
func TestReclaimOnSnapshotReceive(t *testing.T) {
	var hookMu sync.Mutex
	var putCalls, delCalls []string
	opts := DefaultOptions()
	opts.PutHook = func(k ds.Key, v []byte) {
		hookMu.Lock()
		putCalls = append(putCalls, k.String())
		hookMu.Unlock()
	}
	opts.DeleteHook = func(k ds.Key) {
		hookMu.Lock()
		delCalls = append(delCalls, k.String())
		hookMu.Unlock()
	}

	replicas, _, closeReplicas := makeNReplicasSeparateStores(t, 2, opts)
	defer closeReplicas()
	w, recv := replicas[0], replicas[1]
	ctx := context.Background()

	const numKeys = 10
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("recv-key-%d", i))
		if err := w.Put(ctx, keys[i], fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	const numDeleted = 3
	for i := range numDeleted {
		if err := w.Delete(ctx, keys[i]); err != nil {
			t.Fatal(err)
		}
	}

	// Receiver fully syncs *before* compaction: it now holds its own
	// local copy of the entire pre-compaction history, on its own
	// blockstore (not the writer's).
	syncReplicaHeads(t, w, recv, "")
	assertSameKV(t, queryAll(t, w), queryAll(t, recv))

	oldHeads, _, err := recv.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := recv.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}
	if len(dagCIDSet) == 0 {
		t.Fatal("expected the receiver to have walked a non-empty pre-compaction history")
	}
	for c := range dagCIDSet {
		if !blockExists(t, recv, c) {
			t.Fatalf("setup issue: receiver should hold block %s before compaction", c)
		}
	}

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	writerState := queryAll(t, w)

	// Only observe hook activity from here on: merging the snapshot and
	// the reclaim it triggers.
	hookMu.Lock()
	putCalls, delCalls = nil, nil
	hookMu.Unlock()

	syncReplicaHeads(t, w, recv, "")

	recvState := queryAll(t, recv)
	assertSameKV(t, writerState, recvState)

	// The receiver's own copy of the covered history is gone, but its
	// processed-block markers were kept (same trade-off as Compact).
	for c := range dagCIDSet {
		if blockExists(t, recv, c) {
			t.Errorf("expected covered block %s to have been reclaimed on the receiver", c)
		}
		processed, err := recv.isProcessed(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			t.Errorf("expected the processed-block marker for reclaimed block %s to be kept", c)
		}
	}

	// R8, observed end-to-end: merging the snapshot delta fires
	// deleteHook exactly numDeleted times -- once per carried-forward
	// tombstone (ordinary putTombs semantics, unconditional per
	// tombstoned key in a delta, nothing to do with reclaim) -- and
	// putHook zero times (every surviving key's value/priority in the
	// snapshot is identical to what the receiver already had, so
	// setValue's existing no-op short-circuit skips the write). The
	// reclaim purge that follows must not add anything on top of that:
	// if R8 regressed (purgeKeyBlocks re-firing hooks for every
	// unchanged surviving key, or re-firing deleteHook for the
	// already-fully-tombstoned keys), this would observe more than
	// numDeleted deleteHook calls and/or a non-zero putHook count.
	hookMu.Lock()
	gotPut, gotDel := append([]string(nil), putCalls...), append([]string(nil), delCalls...)
	hookMu.Unlock()
	if len(gotPut) != 0 {
		t.Errorf("expected no putHook calls merging+reclaiming an already-converged snapshot, got %v", gotPut)
	}
	if len(gotDel) != numDeleted {
		t.Errorf("expected exactly %d deleteHook calls (the carried-forward tombstones, R8: none extra from reclaim), got %v", numDeleted, gotDel)
	}

	// Generation bookkeeping: counter present, done marker set.
	newHeads, _, err := recv.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(newHeads) != 1 {
		t.Fatalf("expected a single snapshot head after compaction, got %d", len(newHeads))
	}
	total, id := snapshotMetaOf(t, recv, newHeads[0])
	if total == 0 || len(id) == 0 {
		t.Fatal("expected the merged snapshot delta to carry generation metadata")
	}
	if has, err := recv.store.Has(ctx, recv.reclaimCounterKey(id)); err != nil || !has {
		t.Fatalf("expected a reclaim counter entry for generation %x, has=%v err=%v", id, has, err)
	}
	if has, err := recv.store.Has(ctx, recv.reclaimDoneKey(id)); err != nil || !has {
		t.Fatalf("expected a reclaim done marker for generation %x, has=%v err=%v", id, has, err)
	}

	// Values/priorities identical to the writer, per key.
	for _, k := range keys {
		wHas, err := w.Has(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		rHas, err := recv.Has(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		if wHas != rHas {
			t.Fatalf("Has mismatch for %s: writer=%v receiver=%v", k, wHas, rHas)
		}
		if !wHas {
			continue
		}
		wv, err := w.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		rv, err := recv.Get(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		if string(wv) != string(rv) {
			t.Fatalf("value mismatch for %s: writer=%q receiver=%q", k, wv, rv)
		}
		wp, err := w.set.getPriority(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		rp, err := recv.set.getPriority(ctx, k.String())
		if err != nil {
			t.Fatal(err)
		}
		if wp != rp {
			t.Fatalf("priority mismatch for %s: writer=%d receiver=%d", k, wp, rp)
		}
	}
}

// TestReclaimWaitsForAllSiblings is the R10/R11 end-to-end counterpart of
// reclaim_test.go's TestReclaimWaitsForAllSiblingsDirect: the core
// regression test for the counter rule (R4) across two real replicas with
// separate blockstores, delivering each sibling snapshot node to the
// receiver one at a time via a direct handleBlock call (no broadcaster
// timing involved, fully deterministic). Reclaiming must wait until every
// sibling has been merged, otherwise a key whose surviving element lives in
// a not-yet-merged sibling could transiently disappear.
func TestReclaimWaitsForAllSiblings(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxBatchDeltaSize = 40 // bytes: small enough to force multiple siblings.
	replicas, _, closeReplicas := makeNReplicasSeparateStores(t, 2, opts)
	defer closeReplicas()
	w, recv := replicas[0], replicas[1]
	ctx := context.Background()

	const numKeys = 8
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("wait-key-%d", i))
		if err := w.Put(ctx, keys[i], fmt.Appendf(nil, "value-%03d-xxxxxxxxxxxxxxxxxxxx", i)); err != nil {
			t.Fatal(err)
		}
	}

	syncReplicaHeads(t, w, recv, "")
	before := queryAll(t, recv)

	oldHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := recv.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}
	if len(dagCIDSet) == 0 {
		t.Fatal("expected a non-empty pre-compaction history")
	}
	sampleCID := headCIDs(oldHeads)[0]

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	newHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(newHeads) < 2 {
		t.Fatalf("expected multiple sibling snapshot heads with a small MaxBatchDeltaSize, got %d", len(newHeads))
	}

	total, id := snapshotMetaOf(t, w, newHeads[0])
	if int(total) != len(newHeads) {
		t.Fatalf("expected snapshotTotal %d to match sibling count, got %d", len(newHeads), total)
	}

	for i, h := range newHeads {
		if err := recv.handleBlock(ctx, h); err != nil {
			t.Fatal(err)
		}

		done, err := recv.store.Has(ctx, recv.reclaimDoneKey(id))
		if err != nil {
			t.Fatal(err)
		}

		if i < len(newHeads)-1 {
			if done {
				t.Fatalf("expected no done marker after sibling %d/%d", i+1, len(newHeads))
			}
			if !blockExists(t, recv, sampleCID) {
				t.Fatalf("expected covered history to still be intact after sibling %d/%d", i+1, len(newHeads))
			}
			// No visibility gap: every live key must still be readable.
			for _, k := range keys {
				if _, err := recv.Get(ctx, k); err != nil {
					t.Fatalf("key %s unreadable after sibling %d/%d: %v", k, i+1, len(newHeads), err)
				}
			}
		} else {
			if !done {
				t.Fatalf("expected a done marker after the final sibling %d/%d", i+1, len(newHeads))
			}
			for c := range dagCIDSet {
				if blockExists(t, recv, c) {
					t.Errorf("expected covered block %s to have been reclaimed after the final sibling", c)
				}
			}
		}
	}

	after := queryAll(t, recv)
	assertSameKV(t, before, after)
}

// TestReclaimFreshReplica checks that a fresh receiver -- one that never
// synced any of the pre-compaction history, and only ever fetches the
// post-compaction snapshot head(s) -- runs the auto-reclaim trigger to a
// harmless no-op: it never even attempts to touch the covered history (it
// has no processed markers for it, so walkProcessedDAG cannot find
// anything to purge), but the bookkeeping (done marker) is still written so
// a later duplicate delivery does not re-attempt anything.
func TestReclaimFreshReplica(t *testing.T) {
	replicas, _, closeReplicas := makeNReplicasSeparateStores(t, 2, nil)
	defer closeReplicas()
	w, fresh := replicas[0], replicas[1]
	ctx := context.Background()

	for i := range 12 {
		if err := w.Put(ctx, ds.NewKey(fmt.Sprintf("fresh-e2e-key-%d", i)), fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 4 {
		if err := w.Delete(ctx, ds.NewKey(fmt.Sprintf("fresh-e2e-key-%d", i))); err != nil {
			t.Fatal(err)
		}
	}

	oldHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	coveredCIDSet, _, _, err := w.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// fresh has never synced anything before this.
	syncReplicaHeads(t, w, fresh, "")

	writerState := queryAll(t, w)
	freshState := queryAll(t, fresh)
	assertSameKV(t, writerState, freshState)

	// fresh never even touched the covered history: no processed marker
	// for any of it.
	for c := range coveredCIDSet {
		processed, err := fresh.isProcessed(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if processed {
			t.Errorf("fresh replica should never have touched purged history block %s", c)
		}
	}

	heads, _, err := fresh.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 1 {
		t.Fatalf("expected 1 head, got %d", len(heads))
	}
	total, id := snapshotMetaOf(t, fresh, heads[0])
	if total == 0 || len(id) == 0 {
		t.Fatal("expected snapshot metadata on the merged head")
	}
	if has, err := fresh.store.Has(ctx, fresh.reclaimDoneKey(id)); err != nil || !has {
		t.Fatalf("expected a done marker even for a no-op (fresh replica) reclaim, has=%v err=%v", has, err)
	}
}

// TestReclaimCompactedExplicit checks Options.ReclaimOnSnapshot=false across
// two real replicas: the auto-trigger never fires (no counter entry, covered
// history survives merging the snapshot), while ReclaimCompacted remains
// fully functional as the manual path, and is idempotent on a second call.
func TestReclaimCompactedExplicit(t *testing.T) {
	opts := DefaultOptions()
	opts.ReclaimOnSnapshot = false
	replicas, _, closeReplicas := makeNReplicasSeparateStores(t, 2, opts)
	defer closeReplicas()
	w, recv := replicas[0], replicas[1]
	ctx := context.Background()

	for i := range 6 {
		if err := w.Put(ctx, ds.NewKey(fmt.Sprintf("explicit-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	syncReplicaHeads(t, w, recv, "")
	before := queryAll(t, recv)

	oldHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := recv.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	syncReplicaHeads(t, w, recv, "")

	for c := range dagCIDSet {
		if !blockExists(t, recv, c) {
			t.Errorf("expected covered block %s to survive with ReclaimOnSnapshot=false", c)
		}
	}

	heads, _, err := recv.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	_, id := snapshotMetaOf(t, recv, heads[0])
	if has, err := recv.store.Has(ctx, recv.reclaimCounterKey(id)); err != nil {
		t.Fatal(err)
	} else if has {
		t.Error("expected no reclaim counter entry with ReclaimOnSnapshot=false")
	}

	n, err := recv.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(dagCIDSet) {
		t.Fatalf("expected ReclaimCompacted to reclaim %d blocks, got %d", len(dagCIDSet), n)
	}
	for c := range dagCIDSet {
		if blockExists(t, recv, c) {
			t.Errorf("expected covered block %s to have been reclaimed by ReclaimCompacted", c)
		}
	}

	n2, err := recv.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("expected a second ReclaimCompacted call to reclaim 0 blocks, got %d", n2)
	}

	after := queryAll(t, recv)
	assertSameKV(t, before, after)
}

// TestReclaimCrashRecovery simulates the missed-counter crash window
// documented on maybeReclaimOnSnapshot/Compact: a crash between a sibling's
// merge (markProcessed) and its counter increment loses that count forever,
// so the generation's counter never reaches snapshotTotal and the auto-path
// never fires for it. It is reproduced here by injecting a fault into the
// receiver's underlying store that fails exactly the counter-key Put for
// the final sibling's delivery -- the merge itself (and therefore the
// node's processed/head bookkeeping) still succeeds, only the soft reclaim
// step does not -- and, matching the scenario described in the spec, the
// counter key is additionally deleted afterwards, leaving no done marker
// and no counter for the generation at all. ReclaimCompacted (R6) must
// still find and reclaim it.
func TestReclaimCrashRecovery(t *testing.T) {
	ctx := context.Background()

	// writer: its own blockstore, nothing to fetch through (it is always
	// the sync source in this test).
	wBS := mdutils.Bserv()
	wDagsync := &fetchThroughDAGSvc{DAGService: merkledag.NewDAGService(wBS)}
	wOpts := DefaultOptions()
	wOpts.MaxBatchDeltaSize = 40 // force multiple siblings, needed to observe the "waits, then crashes on the last one" window.
	w, err := NewDatastore(dssyncMap(), ds.NewKey("crashtest-w"), wDagsync, &nullBroadcaster{}, wOpts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// receiver: its own blockstore, fetch-through to the writer, and an
	// underlying KV store we can selectively fault.
	rBS := mdutils.Bserv()
	rDagsync := &fetchThroughDAGSvc{DAGService: merkledag.NewDAGService(rBS), remote: wDagsync.DAGService}
	fd := newFaultyDatastore(ds.NewMapDatastore())
	rOpts := DefaultOptions()
	rOpts.MaxBatchDeltaSize = 40
	recv, err := NewDatastore(fd, ds.NewKey("crashtest-r"), rDagsync, &nullBroadcaster{}, rOpts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recv.Close() })

	const numKeys = 8
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("crash-key-%d", i))
		if err := w.Put(ctx, keys[i], fmt.Appendf(nil, "value-%03d-xxxxxxxxxxxxxxxxxxxx", i)); err != nil {
			t.Fatal(err)
		}
	}

	syncReplicaHeads(t, w, recv, "")
	before := queryAll(t, recv)

	oldHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := recv.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}
	if len(dagCIDSet) == 0 {
		t.Fatal("expected a non-empty pre-compaction history")
	}

	if _, err := w.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	newHeads, _, err := w.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(newHeads) < 2 {
		t.Fatalf("expected multiple sibling snapshot heads with a small MaxBatchDeltaSize, got %d", len(newHeads))
	}
	_, id := snapshotMetaOf(t, w, newHeads[0])

	// Deliver every sibling but the last normally.
	for _, h := range newHeads[:len(newHeads)-1] {
		if err := recv.handleBlock(ctx, h); err != nil {
			t.Fatal(err)
		}
	}

	// "Crash" exactly the counter increment for the final sibling: the
	// merge (and its processed/head bookkeeping) must still succeed --
	// only the soft, best-effort reclaim step is allowed to fail.
	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" && strings.Contains(key.String(), "/rc/c/") {
			return errFault
		}
		return nil
	})
	if err := recv.handleBlock(ctx, newHeads[len(newHeads)-1]); err != nil {
		t.Fatalf("expected the merge itself to succeed despite the injected reclaim fault: %v", err)
	}
	fd.SetFail(nil)

	// Matching the crash scenario: the counter key for this generation
	// is gone (never durably incremented past what the earlier,
	// unfaulted siblings wrote, and we clear even that here for a clean
	// "total loss" simulation) and no done marker was ever written.
	if err := recv.store.Delete(ctx, recv.reclaimCounterKey(id)); err != nil && !errors.Is(err, ds.ErrNotFound) {
		t.Fatal(err)
	}
	if has, err := recv.store.Has(ctx, recv.reclaimDoneKey(id)); err != nil || has {
		t.Fatalf("expected no done marker before recovery, has=%v err=%v", has, err)
	}
	for c := range dagCIDSet {
		if !blockExists(t, recv, c) {
			t.Fatalf("expected covered history to have survived the missed increment, block %s is gone", c)
		}
	}

	n, err := recv.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(dagCIDSet) {
		t.Fatalf("expected ReclaimCompacted to recover and reclaim %d blocks, got %d", len(dagCIDSet), n)
	}
	for c := range dagCIDSet {
		if blockExists(t, recv, c) {
			t.Errorf("expected covered block %s to have been reclaimed by the recovery pass", c)
		}
	}
	if has, err := recv.store.Has(ctx, recv.reclaimDoneKey(id)); err != nil || !has {
		t.Fatalf("expected a done marker to be written by the recovery pass, has=%v err=%v", has, err)
	}

	after := queryAll(t, recv)
	assertSameKV(t, before, after)
}
