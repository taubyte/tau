package kvdb

import (
	"testing"
	"time"
)

func TestARemovalThatArrivesExpiredIsNotAppliedWhateverCarriesIt(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	st.away(t, simRetention+2*simMargin, a, b, c)
	if a.knows(w.r.replica) {
		t.Fatal("setup: the writer should be forgotten, /k ancient")
	}
	removal := del(t, a.r, "/k")
	a.out.take()
	p := pendingOf(t, a.r)
	gone := p.expires() + a.r.times.margin
	for a.r.dropClockNow()+1 < gone {
		st.advance(min(simRI, time.Duration(gone-1-a.r.dropClockNow())*time.Millisecond))
		st.heartbeat(t, a, b, c)
	}
	compact(t, a.r)
	st.advance(time.Millisecond)
	st.heartbeat(t, a, b, c)

	b.pull(t, a)
	c.mergeBlock(a, removal)
	if n := countEntries(t, b.r, b.r.set.pendingPrefix()); n != 0 {
		t.Fatalf("b keeps %d pending removals that arrived expired", n)
	}
	b.wantValue(t, "/k", c.value(t, "/k"))
	c.wantValue(t, "/k", "v")
}
