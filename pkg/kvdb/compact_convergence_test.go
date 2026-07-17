package kvdb

import (
	"context"
	"fmt"
	"testing"

	ds "github.com/ipfs/go-datastore"
)

// TestConcurrentCompactSameView pins the concurrent-compaction convergence
// contract: two replicas that compact the SAME fully-synced view (logically
// concurrently -- neither has seen the other's snapshot) converge, and, since
// Compact is deterministic (sorted keys, per-element priorities, covered-head
// derived generation id, no clock/rand), they in fact produce byte-identical
// snapshot blocks with the SAME CID -- content-addressing dedups the race
// into a single generation. A third replica that had merged the original
// history receives that generation and reclaims exactly once.
func TestConcurrentCompactSameView(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 3, nil)
	defer closeReplicas()
	a, b, c := replicas[0], replicas[1], replicas[2]
	// The harness wires b and c to fetch through a; a additionally needs to
	// be able to fetch b's snapshot for the exchange below.
	dagsyncs[0].remote = dagsyncs[1].DAGService
	ctx := context.Background()

	// Deterministic state: single-version keys (one multi-version to
	// exercise winner selection), one deleted key with a single tombstone.
	for i := range 8 {
		if err := a.Put(ctx, ds.NewKey(fmt.Sprintf("cc-key-%d", i)), fmt.Appendf(nil, "v%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Put(ctx, ds.NewKey("cc-key-5"), []byte("v5-final")); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, ds.NewKey("cc-key-3")); err != nil {
		t.Fatal(err)
	}

	syncReplicaHeads(t, a, b, "")
	syncReplicaHeads(t, a, c, "")
	assertSameKV(t, queryAll(t, a), queryAll(t, b))

	// Record the covered history as replica c holds it, to verify its
	// reclamation at the end.
	preHeads, _, err := c.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	coveredOnC, _, _, err := c.walkProcessedDAG(ctx, headCIDs(preHeads))
	if err != nil {
		t.Fatal(err)
	}
	if len(coveredOnC) == 0 {
		t.Fatal("test setup: replica c should hold the full history")
	}

	// Logically-concurrent compactions: both replicas compact the same view,
	// neither having seen the other's snapshot.
	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	headsA, _, err := a.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	headsB, _, err := b.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(headsA) != 1 || len(headsB) != 1 {
		t.Fatalf("expected one snapshot head per replica, got %d and %d", len(headsA), len(headsB))
	}
	// The convergence kicker: deterministic compaction of the same view
	// yields the SAME block on both replicas.
	if !headsA[0].Cid.Equals(headsB[0].Cid) {
		t.Fatalf("concurrent compactions of the same view should produce identical snapshot CIDs, got %s vs %s", headsA[0].Cid, headsB[0].Cid)
	}

	// Exchange: each replica receives the other's snapshot head. Since the
	// CIDs are identical, each side recognizes it as already processed; the
	// race collapses into one generation with no extra heads.
	syncReplicaHeads(t, b, a, "")
	syncReplicaHeads(t, a, b, "")

	headsA, _, err = a.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	headsB, _, err = b.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(headsA) != 1 || len(headsB) != 1 || !headsA[0].Cid.Equals(headsB[0].Cid) {
		t.Fatalf("after exchange both replicas should hold the single deduped snapshot head, got %v and %v", headsA, headsB)
	}
	assertSameKV(t, queryAll(t, a), queryAll(t, b))

	// Third replica: receives the (single) generation, converges, and
	// reclaims its local copy of the covered history exactly once.
	syncReplicaHeads(t, a, c, "")
	assertSameKV(t, queryAll(t, a), queryAll(t, c))

	total, id := snapshotMetaOf(t, c, headsA[0])
	if total != 1 || len(id) == 0 {
		t.Fatalf("expected single-sibling generation with an id, got total=%d id=%x", total, id)
	}
	done, err := c.store.Has(ctx, c.reclaimDoneKey(id))
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("expected replica c to have auto-reclaimed the generation (done marker missing)")
	}
	for cc := range coveredOnC {
		if _, err := dagsyncs[2].DAGService.Get(ctx, cc); err == nil {
			t.Fatalf("covered block %s should have been reclaimed from replica c's local store", cc)
		}
	}
}

// TestCompactConcurrentDeleteWins pins the coordination-free contract
// (compact.go's "Coordination-free" section): compacting concurrently with
// an unseen delete must NOT resurrect the deleted key. A snapshot element
// keeps its original id (see putElems/S2), so the concurrent tombstone
// (which targeted that same original id) still covers it after compaction
// folds its storage into a snapshot block -- exactly as it would have
// covered the pre-compaction element. Replicas converge -- identical heads
// and state everywhere -- with the key DELETED, regardless of which
// direction the exchange happens in. This test used to be
// TestCompactConcurrentDeleteResurrection and pinned the opposite (documented
// anomaly) outcome from when compaction re-homed elements under new ids;
// stable element ids retired that requirement and this test now pins its
// replacement.
func TestCompactConcurrentDeleteWins(t *testing.T) {
	replicas, dagsyncs, closeReplicas := makeNReplicasSeparateStores(t, 2, nil)
	defer closeReplicas()
	a, b := replicas[0], replicas[1]
	dagsyncs[0].remote = dagsyncs[1].DAGService // mutual fetch for the exchange
	ctx := context.Background()

	k := ds.NewKey("contested")
	if err := a.Put(ctx, k, []byte("keep-me")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, ds.NewKey("bystander"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	syncReplicaHeads(t, a, b, "")

	// Partitioned, logically-concurrent operations: b deletes the key
	// (tombstoning the element id it observed) while a compacts (re-homing
	// the key's element under the snapshot's new id).
	if err := b.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if has, err := b.Has(ctx, k); err != nil || has {
		t.Fatalf("delete should have applied on b, has=%v err=%v", has, err)
	}
	if _, err := a.Compact(ctx, ""); err != nil {
		t.Fatal(err)
	}

	// Reconnect: exchange both branches.
	syncReplicaHeads(t, b, a, "")
	syncReplicaHeads(t, a, b, "")

	// Convergence: identical state and identical head sets on both sides.
	assertSameKV(t, queryAll(t, a), queryAll(t, b))
	headsA, _, err := a.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	headsB, _, err := b.heads.ListDAG(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	cidsA := make(map[string]struct{}, len(headsA))
	for _, h := range headsA {
		cidsA[h.Cid.String()] = struct{}{}
	}
	if len(headsA) != len(headsB) {
		t.Fatalf("head count mismatch: %v vs %v", headsA, headsB)
	}
	for _, h := range headsB {
		if _, ok := cidsA[h.Cid.String()]; !ok {
			t.Fatalf("head sets diverged: %v vs %v", headsA, headsB)
		}
	}

	// The coordination-free outcome: the concurrent delete targeted the
	// element's original id, which compaction preserved (only aliasing its
	// storage to the snapshot block), so the tombstone still covers it. The
	// key is DELETED on BOTH replicas, including the one that compacted.
	for name, r := range map[string]*Datastore{"compactor": a, "deleter": b} {
		has, err := r.Has(ctx, k)
		if err != nil {
			t.Fatalf("%s: unexpected error checking contested key: %s", name, err)
		}
		if has {
			v, _ := r.Get(ctx, k)
			t.Fatalf("%s: expected the contested key to stay deleted (coordination-free contract), got value %q", name, v)
		}
	}

	// The bystander key (untouched by the delete) must have survived the
	// compaction/exchange unaffected.
	for name, r := range map[string]*Datastore{"compactor": a, "deleter": b} {
		v, err := r.Get(ctx, ds.NewKey("bystander"))
		if err != nil {
			t.Fatalf("%s: expected bystander key to survive, got err=%v", name, err)
		}
		if string(v) != "x" {
			t.Fatalf("%s: unexpected bystander value %q", name, v)
		}
	}
}
