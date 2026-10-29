package kvdb

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dssync "github.com/ipfs/go-datastore/sync"
)

// TestARetiredRootKeepsWhatAFetchReuses: a fetch reuses the subtrees of
// this replica's snapshot that a peer's state shares. When that snapshot
// retires before the fetch is merged, the subtrees it alone reached become
// the fetch's intents instead of garbage, and the merge then finds the
// whole closure held.
func TestARetiredRootKeepsWhatAFetchReuses(t *testing.T) {
	ctx := context.Background()
	own := newFetchThrough()
	r := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), own, "r: ")
	defer r.Close()
	peerDag := newFetchThrough()
	peer := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), peerDag, "peer: ")
	defer peer.Close()
	link := newRationed(peerDag.DAGService)
	own.setRemotes(link)
	peerDag.setRemotes(own.DAGService)

	// The peer changes more than its snapshot carries, so some of it is
	// fetched, and leaves most of r's subtrees as they were.
	seedKeys(t, r, 1000, "k")
	compact(t, r)
	syncReplicaHeads(t, r, peer)
	for i := range 40 {
		put(t, peer, fmt.Sprintf("/k%04d", i*25), "peer")
	}
	compact(t, peer)
	want := queryAll(t, peer)

	link.refill(6)
	syncHeadsLogged(t, peer, r)
	if !r.IsDirty(ctx) {
		t.Fatal("the first attempt fetched the whole state")
	}
	for i := range 400 {
		if i%3 == 0 {
			put(t, r, fmt.Sprintf("/k%04d", i), "own")
			want[fmt.Sprintf("/k%04d", i)] = []byte("own")
		}
	}
	compact(t, r)
	if _, err := r.reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	rescued := 0
	r.gcMu.Lock()
	for _, s := range r.gc.sessions {
		for _, k := range s.borrowed {
			if s.marks[k] == markIntent {
				rescued++
			}
		}
	}
	r.gcMu.Unlock()
	if rescued == 0 {
		t.Fatal("the retired snapshot left nothing the fetch reused to keep")
	}
	assertHoldsExactly(t, r, own)

	if _, err := attemptCatchUp(t, r, peer, link, 1<<20, 10); err != nil {
		t.Fatal(err)
	}
	compact(t, r)
	if _, err := r.reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got := queryAll(t, r)[k]; string(got) != string(v) && k[1] == 'k' && string(v) == "peer" {
			t.Fatalf("%s: %q, want the peer's %q", k, got, v)
		}
	}
	assertReach(t, r)
	assertHoldsExactly(t, r, own)
	assertTreesMatch(t, r)
	t.Logf("%d reused blocks kept", rescued)
}

// TestAManifestThatCannotDrainFencesAdmissions: while a manifest cannot be
// drained, nothing is admitted, pinned or staged: a compaction fails rather
// than write blocks the manifest may name. Once removals work again, the
// manifest drains and the store holds exactly what its roots reach.
func TestAManifestThatCannotDrainFencesAdmissions(t *testing.T) {
	ctx := context.Background()
	d := &removesFailing{fetchThroughDAGSvc: newFetchThrough()}
	d.allowed.Store(-1)
	r := manualReplica(t, dssync.MutexWrap(ds.NewMapDatastore()), d, "fenced: ")
	defer r.Close()
	seedKeys(t, r, 300, "k")
	compact(t, r)
	root := stateOf(t, r)
	for i := range 30 {
		put(t, r, fmt.Sprintf("/k%04d", i*7), "changed")
	}
	// The purge removes the old snapshot's blocks; the drain of what its
	// state alone reached fails.
	d.allowed.Store(1)
	if _, err := r.Compact(ctx); !errors.Is(err, errFault) {
		t.Fatalf("a compaction whose removals fail: %v", err)
	}
	r.gcMu.Lock()
	pending := len(r.gc.manifests)
	r.gcMu.Unlock()
	if pending == 0 {
		t.Fatal("no manifest is left to drain")
	}
	cur := stateOf(t, r)
	put(t, r, "/after", "x")
	if _, err := r.Compact(ctx); !errors.Is(err, errFault) {
		t.Fatalf("a compaction while a manifest cannot drain: %v", err)
	}
	if _, err := r.pinState(ctx, cur, time.Hour); !errors.Is(err, errFault) {
		t.Fatalf("a pin while a manifest cannot drain: %v", err)
	}
	if r.reaches(root) {
		t.Fatal("the retired state is still reached")
	}

	d.allowed.Store(-1)
	if _, err := r.reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	compact(t, r)
	if _, err := r.reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	if got := string(queryAll(t, r)["/after"]); got != "x" {
		t.Fatalf("/after: %q", got)
	}
	assertReach(t, r)
	assertHoldsExactly(t, r, d.fetchThroughDAGSvc)
}

// removesFailing fails every RemoveMany once allowed of them, when not
// negative, have run.
type removesFailing struct {
	*fetchThroughDAGSvc
	allowed atomic.Int64
}

func (d *removesFailing) RemoveMany(ctx context.Context, cids []cid.Cid) error {
	for {
		n := d.allowed.Load()
		if n < 0 {
			break
		}
		if n == 0 {
			return errFault
		}
		if d.allowed.CompareAndSwap(n, n-1) {
			break
		}
	}
	return d.fetchThroughDAGSvc.RemoveMany(ctx, cids)
}
