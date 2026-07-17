package kvdb

// Direct tests for receiver-side reclamation of compacted history (R1-R9):
// Compact stamping compaction-generation metadata onto its snapshot deltas
// (R2), the counter/done-marker auto-trigger inside processNode (R3/R4),
// reclaimCovered (R5), the explicit ReclaimCompacted recovery API (R6),
// Options.ReclaimOnSnapshot (R7), and purgeKeyBlocks becoming hook-quiet on
// no-ops (R8).
//
// These tests drive the R4 trigger directly (build the exact snapshot
// deltas Compact would, then feed them through processNode with
// allowReclaim=true, exactly like dagWorker would for a remotely-received
// block) on a single replica/store, rather than through two replicas
// syncing over a shared blockstore: with the existing shared-blockstore
// harness, a receiving replica's reclaim walk would always find the
// covered blocks already gone (removed by the same underlying blockstore
// the compacting replica purged), which cannot exercise the successful
// reclaim path at all. See reclaim_e2e_test.go (R10/R11) for the
// per-replica-blockstore harness and the full end-to-end tests that drive
// this across two real, independently-stored replicas; the tests here exist
// to directly convince ourselves the mechanism this stage adds is correct
// in isolation.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// buildSnapshotForCurrentState reproduces exactly what Compact does to turn
// dagName's current live state into one or more snapshot Deltas (R2)
// stamped with generation metadata, without purging or processing anything
// -- letting the caller drive putBlock+processNode itself (see
// processSnapshotDeltas) and observe the R4/R5 trigger in isolation from
// Compact's own (allowReclaim=false) purge path.
func buildSnapshotForCurrentState(t testing.TB, r *Datastore, dagName string) (deltas []Delta, coveredHeads []Head, priority uint64) {
	t.Helper()
	ctx := context.Background()

	heads, maxHeight, err := r.heads.ListDAG(ctx, dagName)
	if err != nil {
		t.Fatal(err)
	}
	hc := headCIDs(heads)

	dagCIDSet, setKeys, _, err := r.walkProcessedDAG(ctx, hc)
	if err != nil {
		t.Fatal(err)
	}

	elements, tombstones, err := r.set.compactSnapshotState(ctx, setKeys, dagCIDSet)
	if err != nil {
		t.Fatal(err)
	}

	priority = maxHeight + 1
	snapshotID := snapshotGenerationID(hc)
	deltas = r.buildSnapshotDeltas(dagName, priority, snapshotID, elements, tombstones)
	return deltas, heads, priority
}

// processSnapshotDeltas writes and processes each delta via processNode
// with the given allowReclaim, simulating what dagWorker (allowReclaim=
// true, a remotely-received block) or addDAGNode/Compact (allowReclaim=
// false) would do upon merging it. Returns the resulting snapshot heads.
func processSnapshotDeltas(t testing.TB, r *Datastore, deltas []Delta, coveredHeads []Head, priority uint64, dagName string, allowReclaim bool) []Head {
	t.Helper()
	ctx := context.Background()
	ng := &crdtNodeGetter{NodeGetter: r.dagService}

	var snapHeads []Head
	for _, d := range deltas {
		node, err := r.putBlock(ctx, coveredHeads, d)
		if err != nil {
			t.Fatal(err)
		}
		snapHead := Head{Cid: node.Cid()}
		snapHead.Height = priority
		snapHead.DAGName = dagName
		if _, err := r.processNode(ctx, ng, snapHead, d, node, allowReclaim); err != nil {
			t.Fatal(err)
		}
		snapHeads = append(snapHeads, snapHead)
	}
	return snapHeads
}

// blockExists reports whether c is still fetchable from r's DAG service.
func blockExists(t testing.TB, r *Datastore, c cid.Cid) bool {
	t.Helper()
	_, err := r.dagService.Get(context.Background(), c)
	return err == nil
}

// TestSnapshotGenerationIDDeterministic checks snapshotGenerationID (R2):
// order-independence (bytewise sort makes it a function of the set, not the
// slice order) and that different input sets produce different ids.
func TestSnapshotGenerationIDDeterministic(t *testing.T) {
	// Build a few distinct CIDs the simple way: nodes for distinct deltas.
	cids := make([]cid.Cid, 4)
	for i := range cids {
		delta := &pbDelta{Delta: &pb.Delta{
			Elements: []*pb.Element{{Key: fmt.Sprintf("k%d", i), Value: []byte("v")}},
			Priority: 1,
		}}
		nd, err := makeNode(delta, nil)
		if err != nil {
			t.Fatal(err)
		}
		cids[i] = nd.Cid()
	}

	id1 := snapshotGenerationID(cids)

	reversed := make([]cid.Cid, len(cids))
	for i, c := range cids {
		reversed[len(cids)-1-i] = c
	}
	id2 := snapshotGenerationID(reversed)
	if !bytes.Equal(id1, id2) {
		t.Errorf("expected snapshotGenerationID to be order-independent, got %x vs %x", id1, id2)
	}

	if len(id1) != 16 {
		t.Errorf("expected a 16-byte id, got %d bytes", len(id1))
	}

	id3 := snapshotGenerationID(cids[:3])
	if bytes.Equal(id1, id3) {
		t.Error("expected different covered-head sets to produce different ids")
	}
}

// TestReclaimCoveredFreshReplica checks that reclaimCovered is a safe no-op
// (0, nil) when none of the given CIDs have ever been processed locally --
// the "fresh replica" case (R11's TestReclaimFreshReplica scenario).
func TestReclaimCoveredFreshReplica(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	// A CID that was never Add()ed/processed on this replica.
	delta := &pbDelta{Delta: &pb.Delta{Elements: []*pb.Element{{Key: "never-seen", Value: []byte("v")}}, Priority: 1}}
	nd, err := makeNode(delta, nil)
	if err != nil {
		t.Fatal(err)
	}

	n, err := r.reclaimCovered(ctx, []cid.Cid{nd.Cid()})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 blocks reclaimed on a fresh replica, got %d", n)
	}
}

// TestReclaimAutoTriggerSingleSibling checks the core R4 auto-trigger
// contract for the common (single-sibling) case: after processNode merges a
// snapshot delta with allowReclaim=true and Options.ReclaimOnSnapshot=true,
// its covered history is purged (blocks removed, processed markers kept,
// same as Compact), the sibling counter reaches snapshotTotal, and the done
// marker is written. Live state is unaffected.
func TestReclaimAutoTriggerSingleSibling(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	const numKeys = 10
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("reclaim-key-%d", i))
		if err := r.Put(ctx, keys[i], fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		if err := r.Delete(ctx, keys[i]); err != nil {
			t.Fatal(err)
		}
	}

	before := queryAll(t, r)

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := r.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}
	if len(dagCIDSet) == 0 {
		t.Fatal("expected pre-snapshot history to walk to at least one block")
	}

	deltas, coveredHeads, priority := buildSnapshotForCurrentState(t, r, "")
	if len(deltas) != 1 {
		t.Fatalf("expected a single sibling for this small amount of data, got %d", len(deltas))
	}
	total, id := deltas[0].SnapshotMeta()
	if total != 1 || len(id) == 0 {
		t.Fatalf("expected SnapshotMeta (1, <non-empty id>), got (%d, %x)", total, id)
	}

	processSnapshotDeltas(t, r, deltas, coveredHeads, priority, "", true)

	// Covered history must be gone from the DAG service, but its
	// processed-block markers kept (same trade-off as Compact).
	for c := range dagCIDSet {
		if blockExists(t, r, c) {
			t.Errorf("expected covered block %s to have been reclaimed", c)
		}
		processed, err := r.isProcessed(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if !processed {
			t.Errorf("expected the processed-block marker for reclaimed block %s to be kept", c)
		}
	}

	// Counter reached total, done marker written.
	counterData, err := r.store.Get(ctx, r.reclaimCounterKey(id))
	if err != nil {
		t.Fatalf("expected a reclaim counter entry for generation %x: %v", id, err)
	}
	if len(counterData) == 0 {
		t.Error("expected a non-empty counter value")
	}
	done, err := r.store.Has(ctx, r.reclaimDoneKey(id))
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Errorf("expected a done marker for generation %x", id)
	}

	after := queryAll(t, r)
	assertSameKV(t, before, after)
}

// TestReclaimWaitsForAllSiblingsDirect is the direct/isolated counterpart
// (see this file's package doc) of the R10/R11 end-to-end regression test
// for the counter rule (R4) -- reclaim_e2e_test.go's
// TestReclaimWaitsForAllSiblings, which drives the same invariant across two
// real replicas with separate blockstores. This version stays on a single
// replica/store, driving processNode directly: reclaiming must wait until
// every sibling snapshot node of a generation has been merged, otherwise a
// key whose surviving element lives in a not-yet-merged sibling could
// transiently disappear. It forces multiple siblings with a small
// MaxBatchDeltaSize, processes them one at a time, and checks that covered
// history and every live key survive intact until (and only until) the last
// sibling is merged.
func TestReclaimWaitsForAllSiblingsDirect(t *testing.T) {
	opts := DefaultOptions()
	opts.MaxBatchDeltaSize = 40 // bytes: small enough to force one item per sibling.
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, opts)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	const numKeys = 8
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("sibling-key-%d", i))
		if err := r.Put(ctx, keys[i], fmt.Appendf(nil, "value-%03d-xxxxxxxxxxxxxxxxxxxx", i)); err != nil {
			t.Fatal(err)
		}
	}

	before := queryAll(t, r)

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := r.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	deltas, coveredHeads, priority := buildSnapshotForCurrentState(t, r, "")
	if len(deltas) < 2 {
		t.Fatalf("expected multiple siblings with a small MaxBatchDeltaSize, got %d", len(deltas))
	}
	total, id := deltas[0].SnapshotMeta()
	if int(total) != len(deltas) {
		t.Fatalf("expected snapshotTotal %d to match sibling count, got %d", len(deltas), total)
	}

	sampleCID := headCIDs(oldHeads)[0]

	ng := &crdtNodeGetter{NodeGetter: r.dagService}
	for i, d := range deltas {
		node, err := r.putBlock(ctx, coveredHeads, d)
		if err != nil {
			t.Fatal(err)
		}
		snapHead := Head{Cid: node.Cid()}
		snapHead.Height = priority
		snapHead.DAGName = ""
		if _, err := r.processNode(ctx, ng, snapHead, d, node, true); err != nil {
			t.Fatal(err)
		}

		done, err := r.store.Has(ctx, r.reclaimDoneKey(id))
		if err != nil {
			t.Fatal(err)
		}

		if i < len(deltas)-1 {
			if done {
				t.Fatalf("expected no done marker after sibling %d/%d", i+1, len(deltas))
			}
			if !blockExists(t, r, sampleCID) {
				t.Fatalf("expected covered history to still be intact after sibling %d/%d", i+1, len(deltas))
			}
			// No visibility gap: every live key must still be readable.
			for _, k := range keys {
				if _, err := r.Get(ctx, k); err != nil {
					t.Fatalf("key %s unreadable after sibling %d/%d: %v", k, i+1, len(deltas), err)
				}
			}
		} else {
			if !done {
				t.Fatalf("expected a done marker after the final sibling %d/%d", i+1, len(deltas))
			}
			for c := range dagCIDSet {
				if blockExists(t, r, c) {
					t.Errorf("expected covered block %s to have been reclaimed after the final sibling", c)
				}
			}
		}
	}

	after := queryAll(t, r)
	assertSameKV(t, before, after)
}

// TestReclaimCompactorNotDoubleReclaiming checks that Compact's own
// processing of the snapshot nodes it just created (allowReclaim=false)
// leaves no reclaim counter/done bookkeeping behind for its own generation:
// Compact purges that history itself via its own code path, so the reclaim
// machinery must stay completely silent for it.
func TestReclaimCompactorNotDoubleReclaiming(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	for i := range 5 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("compactor-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	expectedID := snapshotGenerationID(headCIDs(oldHeads))

	if _, err := r.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	if has, err := r.store.Has(ctx, r.reclaimCounterKey(expectedID)); err != nil {
		t.Fatal(err)
	} else if has {
		t.Error("expected no reclaim counter entry for the compacting replica's own generation")
	}
	if has, err := r.store.Has(ctx, r.reclaimDoneKey(expectedID)); err != nil {
		t.Fatal(err)
	} else if has {
		t.Error("expected no reclaim done marker for the compacting replica's own generation")
	}
}

// TestReclaimOnSnapshotOptionDisabled checks Options.ReclaimOnSnapshot=false
// (R7): the auto-trigger never fires (no counter, no purge), while
// ReclaimCompacted (R6) remains fully functional as the manual path, and is
// idempotent on a second call.
func TestReclaimOnSnapshotOptionDisabled(t *testing.T) {
	opts := DefaultOptions()
	opts.ReclaimOnSnapshot = false
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, opts)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	for i := range 5 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("disabled-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	before := queryAll(t, r)

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := r.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	deltas, coveredHeads, priority := buildSnapshotForCurrentState(t, r, "")
	processSnapshotDeltas(t, r, deltas, coveredHeads, priority, "", true)

	_, id := deltas[0].SnapshotMeta()

	for c := range dagCIDSet {
		if !blockExists(t, r, c) {
			t.Errorf("expected covered block %s to survive with ReclaimOnSnapshot=false", c)
		}
	}
	if has, err := r.store.Has(ctx, r.reclaimCounterKey(id)); err != nil {
		t.Fatal(err)
	} else if has {
		t.Error("expected no reclaim counter entry with ReclaimOnSnapshot=false")
	}

	n, err := r.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(dagCIDSet) {
		t.Fatalf("expected ReclaimCompacted to reclaim %d blocks, got %d", len(dagCIDSet), n)
	}
	for c := range dagCIDSet {
		if blockExists(t, r, c) {
			t.Errorf("expected covered block %s to have been reclaimed by ReclaimCompacted", c)
		}
	}

	n2, err := r.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("expected a second ReclaimCompacted call to reclaim 0 blocks, got %d", n2)
	}

	after := queryAll(t, r)
	assertSameKV(t, before, after)
}

// TestReclaimLegacySnapshot checks the legacy-snapshot path: a snapshot
// delta with empty snapshotId/snapshotTotal (as produced by a pre-R1/R2
// version of this package) is never auto-reclaimed, but ReclaimCompacted
// still finds and reclaims it, deriving its generation id from its
// covered-heads links exactly as Compact would have computed it.
func TestReclaimLegacySnapshot(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	for i := range 5 {
		if err := r.Put(ctx, ds.NewKey(fmt.Sprintf("legacy-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	before := queryAll(t, r)

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := r.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}

	deltas, coveredHeads, priority := buildSnapshotForCurrentState(t, r, "")
	if len(deltas) != 1 {
		t.Fatalf("expected a single sibling, got %d", len(deltas))
	}
	// Hand-build a legacy snapshot: strip the metadata this stage adds.
	deltas[0].SetSnapshotMeta(0, nil)

	processSnapshotDeltas(t, r, deltas, coveredHeads, priority, "", true)

	for c := range dagCIDSet {
		if !blockExists(t, r, c) {
			t.Errorf("expected covered block %s to survive the legacy snapshot's auto path (should be skipped)", c)
		}
	}

	expectedID := snapshotGenerationID(headCIDs(oldHeads))
	n, err := r.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(dagCIDSet) {
		t.Fatalf("expected ReclaimCompacted to reclaim %d blocks, got %d", len(dagCIDSet), n)
	}
	for c := range dagCIDSet {
		if blockExists(t, r, c) {
			t.Errorf("expected covered block %s to have been reclaimed", c)
		}
	}
	done, err := r.store.Has(ctx, r.reclaimDoneKey(expectedID))
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Errorf("expected the done marker to be written under the derived legacy id %x", expectedID)
	}

	after := queryAll(t, r)
	assertSameKV(t, before, after)
}

// TestReclaimCompactedWaitsForAllSiblings is the manual-path (R6) counterpart
// of TestReclaimWaitsForAllSiblingsDirect: ReclaimCompacted must apply the
// same "wait for every sibling" rule the auto-path counter (R4) enforces.
// With only some siblings of a multi-sibling generation merged locally,
// calling ReclaimCompacted must reclaim nothing (and keep every live key
// readable) -- purging the covered history early would drop a key whose
// surviving value lives in the not-yet-merged sibling. Once the last sibling
// is merged, ReclaimCompacted reclaims the whole generation.
func TestReclaimCompactedWaitsForAllSiblings(t *testing.T) {
	opts := DefaultOptions()
	// Disable the auto path so ReclaimCompacted is the ONLY thing that can
	// reclaim here, isolating the manual-path completeness rule.
	opts.ReclaimOnSnapshot = false
	opts.MaxBatchDeltaSize = 40 // bytes: small enough to force one item per sibling.
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, opts)
	defer closeReplicas()
	r := replicas[0]
	ctx := context.Background()

	const numKeys = 8
	keys := make([]ds.Key, numKeys)
	for i := range numKeys {
		keys[i] = ds.NewKey(fmt.Sprintf("mp-sibling-key-%d", i))
		if err := r.Put(ctx, keys[i], fmt.Appendf(nil, "value-%03d-xxxxxxxxxxxxxxxxxxxx", i)); err != nil {
			t.Fatal(err)
		}
	}

	before := queryAll(t, r)

	oldHeads, _, err := r.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	dagCIDSet, _, _, err := r.walkProcessedDAG(ctx, headCIDs(oldHeads))
	if err != nil {
		t.Fatal(err)
	}
	sampleCID := headCIDs(oldHeads)[0]

	deltas, coveredHeads, priority := buildSnapshotForCurrentState(t, r, "")
	if len(deltas) < 2 {
		t.Fatalf("expected multiple siblings with a small MaxBatchDeltaSize, got %d", len(deltas))
	}
	_, id := deltas[0].SnapshotMeta()

	ng := &crdtNodeGetter{NodeGetter: r.dagService}
	processOne := func(d Delta) {
		node, err := r.putBlock(ctx, coveredHeads, d)
		if err != nil {
			t.Fatal(err)
		}
		snapHead := Head{Cid: node.Cid()}
		snapHead.Height = priority
		if _, err := r.processNode(ctx, ng, snapHead, d, node, false); err != nil {
			t.Fatal(err)
		}
	}

	// Merge every sibling but the last.
	for _, d := range deltas[:len(deltas)-1] {
		processOne(d)
	}

	// Manual reclaim on an incomplete generation must be a no-op: nothing
	// reclaimed, no done marker, covered history intact, every key readable.
	n, err := r.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected ReclaimCompacted to reclaim nothing for an incomplete generation, got %d", n)
	}
	if has, err := r.store.Has(ctx, r.reclaimDoneKey(id)); err != nil || has {
		t.Fatalf("expected no done marker for an incomplete generation, has=%v err=%v", has, err)
	}
	if !blockExists(t, r, sampleCID) {
		t.Fatal("expected covered history to still be intact while a sibling is missing")
	}
	for _, k := range keys {
		if _, err := r.Get(ctx, k); err != nil {
			t.Fatalf("no visibility gap allowed: key %s unreadable with a sibling missing: %v", k, err)
		}
	}

	// Merge the final sibling, then ReclaimCompacted reclaims the whole
	// generation.
	processOne(deltas[len(deltas)-1])

	n, err = r.ReclaimCompacted(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if n != len(dagCIDSet) {
		t.Fatalf("expected ReclaimCompacted to reclaim %d blocks once complete, got %d", len(dagCIDSet), n)
	}
	for c := range dagCIDSet {
		if blockExists(t, r, c) {
			t.Errorf("expected covered block %s to have been reclaimed once complete", c)
		}
	}
	if has, err := r.store.Has(ctx, r.reclaimDoneKey(id)); err != nil || !has {
		t.Fatalf("expected a done marker after reclaiming the complete generation, has=%v err=%v", has, err)
	}

	after := queryAll(t, r)
	assertSameKV(t, before, after)
}

// TestPurgeKeyBlocksHookQuiet directly checks R8: purgeKeyBlocks must not
// fire putHook when a surviving key's value/priority are unchanged, and
// must not fire deleteHook when the key's value entry was already absent.
// A real change must still always fire its hook exactly once.
func TestPurgeKeyBlocksHookQuiet(t *testing.T) {
	var putCalls, delCalls []string
	opts := DefaultOptions()
	opts.PutHook = func(k ds.Key, v []byte) { putCalls = append(putCalls, k.String()) }
	opts.DeleteHook = func(k ds.Key) { delCalls = append(delCalls, k.String()) }

	d, err := NewDatastore(dssyncMap(), ds.NewKey("hookquiet-test"), newTestDagsync(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()

	k := ds.NewKey("hq-key")
	if err := d.Put(ctx, k, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	h1, _, err := d.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	firstCID := h1[0].Cid

	if err := d.Put(ctx, k, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	h2, _, err := d.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	secondCID := h2[0].Cid

	putCalls, delCalls = nil, nil

	// Purge only the first (older, non-winning) block: the surviving best
	// value/priority (from the second block) are completely unchanged ->
	// no putHook, no deleteHook.
	purge1 := map[cid.Cid]struct{}{firstCID: {}}
	if err := d.set.purgeKeyBlocks(ctx, k.String(), purge1, true, false); err != nil {
		t.Fatal(err)
	}
	if len(putCalls) != 0 {
		t.Errorf("expected no putHook calls for an unchanged surviving value, got %v", putCalls)
	}
	if len(delCalls) != 0 {
		t.Errorf("expected no deleteHook calls, got %v", delCalls)
	}
	got, err := d.Get(ctx, k)
	if err != nil || string(got) != "v2" {
		t.Fatalf("expected value v2 to survive, got %q err=%v", got, err)
	}

	// Purge the last surviving block: this is a real change (v2 -> gone) ->
	// deleteHook must fire exactly once.
	purge2 := map[cid.Cid]struct{}{secondCID: {}}
	if err := d.set.purgeKeyBlocks(ctx, k.String(), purge2, true, false); err != nil {
		t.Fatal(err)
	}
	if len(putCalls) != 0 {
		t.Errorf("expected still no putHook calls, got %v", putCalls)
	}
	if len(delCalls) != 1 {
		t.Fatalf("expected exactly one deleteHook call for the real deletion, got %v", delCalls)
	}
	if has, err := d.Has(ctx, k); err != nil || has {
		t.Fatalf("expected key to be gone, has=%v err=%v", has, err)
	}

	// Purge again with the value key already absent: must not re-fire
	// deleteHook.
	if err := d.set.purgeKeyBlocks(ctx, k.String(), purge2, true, false); err != nil {
		t.Fatal(err)
	}
	if len(delCalls) != 1 {
		t.Errorf("expected deleteHook to stay quiet on an already-absent key, got %v", delCalls)
	}
}

// TestReclaimCompactedNoHeads checks ReclaimCompacted's empty-dagName
// branch: no heads means nothing to walk, and it returns (0, nil) rather
// than erroring.
func TestReclaimCompactedNoHeads(t *testing.T) {
	replicas, closeReplicas := makeNReplicasNoBcast(t, 1, nil)
	defer closeReplicas()
	r := replicas[0]

	n, err := r.ReclaimCompacted(context.Background(), "no-such-dag")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 blocks reclaimed for a dagName with no heads, got %d", n)
	}
}

// TestIncrReclaimGenerationErrors checks incrReclaimGeneration's two error
// branches directly: a Get failure (other than not-found) and a Put
// failure both propagate rather than being swallowed (the soft-failure
// handling lives one level up, in maybeReclaimOnSnapshot).
func TestIncrReclaimGenerationErrors(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()
	id := []byte{1, 2, 3, 4}

	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Get" && strings.Contains(key.String(), "/rc/c/") {
			return errFault
		}
		return nil
	})
	if _, err := d.incrReclaimGeneration(ctx, id); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from the counter Get, got %v", err)
	}

	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" && strings.Contains(key.String(), "/rc/c/") {
			return errFault
		}
		return nil
	})
	if _, err := d.incrReclaimGeneration(ctx, id); !errors.Is(err, errFault) {
		t.Fatalf("expected errFault from the counter Put, got %v", err)
	}
}

// TestMaybeReclaimOnSnapshotSoftFailuresAndIdempotency drives
// maybeReclaimOnSnapshot directly through every one of its soft-failure
// branches (counter-increment error, done-marker read error, done-marker
// write error) and checks each leaves state exactly as documented (nothing
// purged, nothing recorded) before finally succeeding, plus the
// done-marker-already-set idempotency skip on a duplicate delivery of the
// same snapshot node.
func TestMaybeReclaimOnSnapshotSoftFailuresAndIdempotency(t *testing.T) {
	fd := newFaultyDatastore(ds.NewMapDatastore())
	d := newTestDatastore(t, fd)
	ctx := context.Background()

	for i := range 3 {
		if err := d.Put(ctx, ds.NewKey(fmt.Sprintf("soft-key-%d", i)), []byte("v")); err != nil {
			t.Fatal(err)
		}
	}

	deltas, coveredHeads, priority := buildSnapshotForCurrentState(t, d, "")
	if len(deltas) != 1 {
		t.Fatalf("expected a single sibling, got %d", len(deltas))
	}
	total, id := deltas[0].SnapshotMeta()
	if total != 1 {
		t.Fatalf("expected snapshotTotal 1, got %d", total)
	}

	node, err := d.putBlock(ctx, coveredHeads, deltas[0])
	if err != nil {
		t.Fatal(err)
	}

	// 1. Counter Put fails: soft failure, nothing purged or persisted.
	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" && strings.Contains(key.String(), "/rc/c/") {
			return errFault
		}
		return nil
	})
	d.maybeReclaimOnSnapshot(ctx, deltas[0], node)
	fd.SetFail(nil)
	if has, err := d.store.Has(ctx, d.reclaimDoneKey(id)); err != nil || has {
		t.Fatalf("expected no done marker after a counter-increment failure, has=%v err=%v", has, err)
	}
	for _, l := range node.Links() {
		if !blockExists(t, d, l.Cid) {
			t.Fatalf("expected covered block %s to survive a counter-increment failure", l.Cid)
		}
	}

	// 2. Done-key Has() fails: soft failure, still nothing purged.
	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Has" && strings.Contains(key.String(), "/rc/d/") {
			return errFault
		}
		return nil
	})
	d.maybeReclaimOnSnapshot(ctx, deltas[0], node)
	fd.SetFail(nil)
	if has, err := d.store.Has(ctx, d.reclaimDoneKey(id)); err != nil || has {
		t.Fatalf("expected still no done marker after a done-check failure, has=%v err=%v", has, err)
	}
	for _, l := range node.Links() {
		if !blockExists(t, d, l.Cid) {
			t.Fatalf("expected covered block %s to survive a done-check failure", l.Cid)
		}
	}

	// 3. Done-marker Put fails: the reclaim itself still succeeds (blocks
	// purged), only the marker write is lost.
	fd.SetFail(func(op string, key ds.Key) error {
		if op == "Put" && strings.Contains(key.String(), "/rc/d/") {
			return errFault
		}
		return nil
	})
	d.maybeReclaimOnSnapshot(ctx, deltas[0], node)
	fd.SetFail(nil)
	if has, err := d.store.Has(ctx, d.reclaimDoneKey(id)); err != nil || has {
		t.Fatalf("expected no done marker when writing it failed, has=%v err=%v", has, err)
	}
	for _, l := range node.Links() {
		if blockExists(t, d, l.Cid) {
			t.Errorf("expected covered block %s to be reclaimed despite the done-marker write failing", l.Cid)
		}
	}

	// 4. Simulate the write from step 3 having actually landed (a
	// reasonable stand-in for "retried and succeeded"), then re-deliver
	// the exact same snapshot node -- as a duplicate broadcast/rebroadcast
	// would -- and check the done-marker-already-set skip inside
	// maybeReclaimOnSnapshot protects it: it must return quietly without
	// attempting to re-walk the now-purged covered history.
	if err := d.store.Put(ctx, d.reclaimDoneKey(id), nil); err != nil {
		t.Fatal(err)
	}
	ng := &crdtNodeGetter{NodeGetter: d.dagService}
	snapHead := Head{Cid: node.Cid()}
	snapHead.Height = priority
	snapHead.DAGName = ""
	if _, err := d.processNode(ctx, ng, snapHead, deltas[0], node, true); err != nil {
		t.Fatalf("expected reprocessing an already-done generation to succeed quietly, got %v", err)
	}
}

// BenchmarkReclaim measures the R4 auto-trigger's cost -- merging a
// single-sibling snapshot delta with allowReclaim=true, including the
// reclaimCovered purge it triggers -- over a store with 500 keys. Unlike
// BenchmarkCompact (which measures building the snapshot generation itself),
// this isolates the cost of the receive-side merge+reclaim path a remote
// replica actually pays.
func BenchmarkReclaim(b *testing.B) {
	const numKeys = 500
	ctx := context.Background()

	for range b.N {
		b.StopTimer()
		replicas, closeReplicas := makeNReplicasNoBcast(b, 1, nil)
		r := replicas[0]

		crdtBatch, err := r.Batch(ctx)
		if err != nil {
			b.Fatal(err)
		}
		for k := range numKeys {
			key := ds.NewKey(fmt.Sprintf("bench-reclaim-%d", k))
			if err := crdtBatch.Put(ctx, key, []byte("value")); err != nil {
				b.Fatal(err)
			}
		}
		if err := crdtBatch.Commit(ctx); err != nil {
			b.Fatal(err)
		}

		deltas, coveredHeads, priority := buildSnapshotForCurrentState(b, r, "")
		node, err := r.putBlock(ctx, coveredHeads, deltas[0])
		if err != nil {
			b.Fatal(err)
		}
		snapHead := Head{Cid: node.Cid()}
		snapHead.Height = priority
		snapHead.DAGName = ""
		ng := &crdtNodeGetter{NodeGetter: r.dagService}
		b.StartTimer()

		if _, err := r.processNode(ctx, ng, snapHead, deltas[0], node, true); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		closeReplicas()
		b.StartTimer()
	}
}
