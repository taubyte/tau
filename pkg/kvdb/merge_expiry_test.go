package kvdb

import (
	"testing"
	"time"
)

// TestAMergeForgetsWhatExpiredBeforeItReads (M4): what a replica's drop
// clock says has expired is gone before a merge reads its context, whether
// or not maintenance has run since. A removal's entry kept past its expiry
// would refuse the element a peer that forgot the removal holds, ancient,
// and the two would read differently.
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
	// What the merge forgot, the next maintenance compacts away.
	a.maintain(t)
	if hs, _ := a.r.heads.List(); len(hs) != 1 || hs[0].Dot.replica != lastDot(a.r).replica {
		t.Fatalf("what a merge forgot is still in the heads it carries: %v", hs)
	}
}
