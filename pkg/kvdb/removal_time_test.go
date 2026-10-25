package kvdb

import (
	"testing"
	"time"
)

// TestARemovalOfAnExpiredEntryIsPendingWhateverTheReceiversClock (M6): a
// removal of a dot whose entry had expired by the time the removal was
// written is filed as a pending removal, as a replica that forgot the entry
// files it, even by a replica whose drop clock has not reached that expiry
// yet. Touching the entry instead would keep it, and the coverage it gives
// every other dot of the incarnation, alive for another T.
func TestARemovalOfAnExpiredEntryIsPendingWhateverTheReceiversClock(t *testing.T) {
	st := newSimTime()
	w, a, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "r", -simS/2)
	put(t, w.r, "/k", "v")
	put(t, w.r, "/j", "u")
	a.pull(t, w)
	r.pull(t, w)
	a.r.mu.Lock()
	expires := a.r.cc.inc[w.r.replica].expires + a.r.times.margin
	a.r.mu.Unlock()
	for a.r.dropClockNow() < expires {
		st.advance(min(simRI, time.Duration(expires-a.r.dropClockNow())*time.Millisecond))
		st.heartbeat(t, a, r)
		a.maintain(t)
		r.maintain(t)
	}
	if a.knows(w.r.replica) || !r.knows(w.r.replica) {
		t.Fatal("setup: a should have forgotten the writer, r not yet")
	}
	del(t, a.r, "/k")
	r.pull(t, a)
	r.wantValue(t, "/k", "<deleted>")
	if n := countEntries(t, r.r, r.r.set.pendingPrefix()); n != 1 {
		t.Fatalf("r keeps %d pending removals of /k, want 1 as a does", n)
	}
	st.advance(simS)
	st.heartbeat(t, a, r)
	r.maintain(t)
	if r.knows(w.r.replica) {
		t.Fatal("the removal kept alive an entry that had expired before it was written")
	}
	r.wantValue(t, "/j", "u")
}
