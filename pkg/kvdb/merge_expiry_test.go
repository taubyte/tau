package kvdb

import (
	"testing"
	"time"
)

func TestAMergeForgetsWhatExpiredBeforeItReads(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, w.r, "/k", "v")
	a.pull(t, w)
	b.pull(t, w)
	st.advance(time.Second)
	del(t, a.r, "/k")

	a.r.mu.Lock()
	expires := a.r.cc.inc[w.r.replica].expires
	a.r.mu.Unlock()
	for a.r.dropClockNow() < expires+a.r.times.margin {
		st.advance(simRI)
		st.heartbeat(t, a, b)
		b.maintain(t)
	}
	if b.knows(w.r.replica) {
		t.Fatal("setup: b should have forgotten the writer and compacted, /k ancient")
	}
	b.wantValue(t, "/k", "v")
	a.pull(t, b)
	a.wantValue(t, "/k", "v")
	a.maintain(t)
	if hs, _ := a.r.heads.List(); len(hs) != 1 || hs[0].Dot.replica != lastDot(a.r).replica {
		t.Fatalf("what a merge forgot is still in the heads it carries: %v", hs)
	}
}

func TestWhatAMergeForgetsIsCompactedAway(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, w.r, "/x", "1")
	a.pull(t, w)
	a.r.mu.Lock()
	expires := a.r.cc.inc[w.r.replica].expires + a.r.times.margin
	a.r.mu.Unlock()
	for a.r.dropClockNow()+1 < expires {
		st.advance(min(simRI, time.Duration(expires-1-a.r.dropClockNow())*time.Millisecond))
		st.heartbeat(t, a, b)
	}
	a.refresh(t)
	put(t, b.r, "/y", "2")
	st.advance(time.Millisecond)
	st.heartbeat(t, a, b)
	a.pull(t, b)
	if a.knows(w.r.replica) {
		t.Fatal("setup: the merge should have forgotten the writer")
	}
	before := lastDot(a.r)
	a.maintain(t)
	if lastDot(a.r) == before {
		t.Fatal("a forgot an entry in a merge and its next maintenance wrote no snapshot")
	}
	a.wantValue(t, "/x", "1")
}
