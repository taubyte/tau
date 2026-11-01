package kvdb

import "testing"

func TestAnErasedDotIsKeptAcrossRebasesAndExpires(t *testing.T) {
	st := newSimTime()
	j, r, w := st.replica(t, "j", 0), st.replica(t, "r", 0), st.replica(t, "w", 0)
	put(t, r.r, "/x", "removed")
	w.pull(t, r)
	del(t, r.r, "/x")
	j.pull(t, r)
	compact(t, j.r)
	r.pull(t, j)
	if n := countEntries(t, r.r, r.r.ownPrefix()); n != 0 {
		t.Fatalf("setup: %d own operations still logged", n)
	}
	first := r.incarnation()

	st.advance(simRetention + 2*simMargin)
	st.heartbeat(t, w)
	w.maintain(t)
	if w.knows(first) {
		t.Fatal("setup: w still knows the writer of its copy")
	}
	r.dropForRebase(t, st.ms())
	r.reopen(t)
	r.dropForRebase(t, st.ms())
	r.reopen(t)
	r.dropForRebase(t, st.ms())
	r.walked(t)
	r.maintain(t)
	r.r.mu.Lock()
	e := r.r.erased[first]
	r.r.mu.Unlock()
	if e == nil || !e.covers(1) {
		t.Fatalf("after later rebases and reopens, the first one's erased dots are %v", e)
	}

	compact(t, w.r)
	r.pull(t, w)
	r.wantValue(t, "/x", "<deleted>")
	r.maintain(t)
	w.pull(t, r)
	w.wantValue(t, "/x", "<deleted>")

	st.advance(simRetention + 2*simMargin)
	r.maintain(t)
	r.r.mu.Lock()
	left := len(r.r.erased)
	r.r.mu.Unlock()
	if n := countEntries(t, r.r, r.r.erasedPrefix()); left != 0 || n != 0 {
		t.Fatalf("expired erased records kept: %d in memory, %d stored", left, n)
	}
	if r.horizon() < e.changed {
		t.Fatalf("horizon %d is before the forgotten erased record's time %d", r.horizon(), e.changed)
	}
}

func TestAnErasedCopyBesideAnOwnHeadIsCarriedAway(t *testing.T) {
	st := newSimTime()
	j, r, w := st.replica(t, "j", 0), st.replica(t, "r", 0), st.replica(t, "w", 0)
	put(t, r.r, "/x", "removed")
	w.pull(t, r)
	del(t, r.r, "/x")
	j.pull(t, r)
	compact(t, j.r)
	r.pull(t, j)
	first := r.incarnation()

	st.advance(simRetention + 2*simMargin)
	st.heartbeat(t, w)
	w.maintain(t)
	if w.knows(first) {
		t.Fatal("setup: w still knows the writer of its copy")
	}
	r.dropForRebase(t, st.ms())
	r.walked(t)
	r.maintain(t)

	put(t, r.r, "/y", "mine")
	compact(t, w.r)
	r.pull(t, w)
	r.wantValue(t, "/x", "<deleted>")
	if hs, _ := r.r.heads.List(); len(hs) < 2 {
		t.Fatal("setup: r's own head should stand beside w's snapshot")
	}
	r.maintain(t)
	w.pull(t, r)
	w.wantValue(t, "/x", "<deleted>")
}

func TestAnErasedRecordAboutToExpireMakesAnOldSnapshotStale(t *testing.T) {
	st := newSimTime()
	j, r, w := st.replica(t, "j", 0), st.replica(t, "r", 0), st.replica(t, "w", 0)
	put(t, r.r, "/x", "removed")
	w.pull(t, r)
	del(t, r.r, "/x")
	j.pull(t, r)
	compact(t, j.r)
	r.pull(t, j)
	r.dropForRebase(t, st.ms())

	st.advance(simRetention + simMargin/2)
	compact(t, w.r)
	old := onlyHeadLink(t, w.r)
	r.pull(t, w)
	if r.r.covered(old.dot) {
		t.Fatalf("a snapshot from before the rebase was merged as its erased dots expire")
	}
}
