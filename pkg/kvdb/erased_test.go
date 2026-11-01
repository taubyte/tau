package kvdb

import (
	"context"
	"testing"
	"time"
)

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
