package kvdb

import (
	"context"
	"testing"
	"time"
)

// TestARebaseClaimsNoBlockItDidNotMerge (M1): the judge's head is the
// rebased replica's own old snapshot, which holds the judge's write. The
// rebase must walk it: a context that claimed it would skip it, and the
// snapshot the replica writes next would tell the judge it holds that state,
// so the judge drops the head and keeps a write no head represents.
func TestARebaseClaimsNoBlockItDidNotMerge(t *testing.T) {
	ctx := context.Background()
	st := newSimTime()
	j, r := st.replica(t, "j", 0), st.replica(t, "r", 0)
	put(t, j.r, "/b", "theirs")
	r.pull(t, j)
	put(t, r.r, "/a", "mine")
	compact(t, r.r)
	old := onlyHeadLink(t, r.r)
	j.pull(t, r)
	if onlyHead(t, j.r) != old.cid {
		t.Fatal("setup: the judge's head is not the replica's snapshot")
	}

	r.dropForRebase(t, st.ms())
	if r.r.covered(old.dot) {
		t.Errorf("the rebase's context claims %s, a snapshot it has not merged", old.dot)
	}
	hs, _ := j.r.heads.List()
	r.reach(j)
	if err := r.r.rebuild(ctx, r.r.currentGeneration(), 0, message{heads: hs}); err != nil {
		t.Fatal(err)
	}
	j.pull(t, r)

	if !sameCids(headCIDs(listHeads(j.r)), headCIDs(listHeads(r.r))) {
		t.Fatal("the judge and the rebased replica do not end on the same heads")
	}
	assertSameKV(t, queryAll(t, j.r), queryAll(t, r.r))
	r.wantValue(t, "/b", "theirs")
	r.wantValue(t, "/a", "mine")
}

func listHeads(r *Datastore) []Head {
	hs, _ := r.heads.List()
	return hs
}

// TestAStaleForeignHeadIsRefreshed (P3): a head that is another
// incarnation's snapshot, which this replica would now refuse as stale, is
// folded into a snapshot of its own: a replica that knows what it knows
// would refuse that head too, and nothing else here carries its state.
func TestAStaleForeignHeadIsRefreshed(t *testing.T) {
	ctx := context.Background()
	st := newSimTime()
	x, j, c := st.replica(t, "x", 0), st.replica(t, "j", 0), st.replica(t, "c", 0)
	st.heartbeat(t, x)
	heard := st.ms()
	st.advance(5 * time.Second)
	put(t, x.r, "/k", "v")
	compact(t, x.r)
	st.heartbeat(t, j, c)
	j.pull(t, x)
	if onlyHeadLink(t, j.r).dot.replica != x.r.replica {
		t.Fatal("setup: j's head is not x's snapshot")
	}

	// j learns that something written after x last heard the fleet has
	// been forgotten: x's snapshot may hold what that removed.
	for _, sr := range []*simReplica{j, c} {
		sr.r.mu.Lock()
		err := sr.r.hearHorizon(ctx, heard)
		sr.r.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	j.maintain(t)
	if h := onlyHeadLink(t, j.r); h.dot.replica != j.r.replica {
		t.Fatalf("j's head is still %s, a snapshot it would refuse", h.dot)
	}
	c.pull(t, j)
	c.wantValue(t, "/k", "v")
}

// TestAnOwnHeadWrittenBehindTheFleetIsRefreshed (P3): a snapshot this
// replica wrote before it heard the fleet again carries the fleet time it
// had then. Once that is stale by what it knows, a peer that knows as much
// refuses it, so it is replaced by one that carries the fleet time it has
// now.
func TestAnOwnHeadWrittenBehindTheFleetIsRefreshed(t *testing.T) {
	ctx := context.Background()
	st := newSimTime()
	x, c := st.replica(t, "x", 0), st.replica(t, "c", 0)
	st.heartbeat(t, x)
	heard := st.ms()
	st.advance(10 * time.Second)
	put(t, x.r, "/k", "v")
	compact(t, x.r)
	old := onlyHead(t, x.r)
	st.advance(10 * time.Second)
	st.heartbeat(t, x, c)
	for _, sr := range []*simReplica{x, c} {
		sr.r.mu.Lock()
		err := sr.r.hearHorizon(ctx, heard)
		sr.r.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	x.maintain(t)
	if onlyHead(t, x.r) == old {
		t.Fatal("x's head is still the snapshot it wrote behind the fleet")
	}
	c.pull(t, x)
	c.wantValue(t, "/k", "v")
}

// TestAHeadIsNotRefreshedIntoOneAsStale: a stale head snapshot is replaced
// only by one that is not: not while the replica rejoins, when what it
// writes carries the fleet time it rejoins from, nor while the fleet time
// it has heard is itself stale by what it knows. Refreshing then would
// write another such snapshot at every pass.
func TestAHeadIsNotRefreshedIntoOneAsStale(t *testing.T) {
	ctx := context.Background()
	st := newSimTime()
	x := st.replica(t, "x", 0)
	st.heartbeat(t, x)
	heard := st.ms()
	put(t, x.r, "/k", "v")
	compact(t, x.r)
	horizon := func(h uint64) {
		x.r.mu.Lock()
		err := x.r.hearHorizon(ctx, h)
		x.r.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	horizon(heard)
	head := onlyHead(t, x.r)
	for range 3 {
		st.advance(simRI)
		x.maintain(t)
	}
	if onlyHead(t, x.r) != head {
		t.Fatal("a head was refreshed while the fleet time this replica heard is stale")
	}

	st.advance(10 * time.Second)
	st.heartbeat(t, x)
	x.reopen(t)
	if !x.rejoining() {
		t.Fatal("setup: x should rejoin")
	}
	for range 3 {
		st.advance(simRI)
		x.maintain(t)
	}
	if onlyHead(t, x.r) != head {
		t.Fatal("a head was refreshed while rejoining")
	}
}
