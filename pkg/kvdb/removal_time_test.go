package kvdb

import (
	"slices"
	"testing"
	"time"
)

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

func TestARemovalOfAnExpiredEntryStaysPendingWhenItsDeltaArrivesAfter(t *testing.T) {
	st := newSimTime()
	w, a := st.replica(t, "w", 0), st.replica(t, "a", 0)
	first, late := st.replica(t, "first", -simS/2), st.replica(t, "late", -simS/2)
	add := put(t, w.r, "/k", "v")
	a.pull(t, w)
	first.mergeBlock(w, add)
	a.r.mu.Lock()
	expires := a.r.cc.inc[w.r.replica].expires + a.r.times.margin
	a.r.mu.Unlock()
	for a.r.dropClockNow() < expires {
		st.advance(min(simRI, time.Duration(expires-a.r.dropClockNow())*time.Millisecond))
		st.heartbeat(t, a, first, late)
		a.maintain(t)
		first.maintain(t)
		late.maintain(t)
	}
	if a.knows(w.r.replica) || !first.knows(w.r.replica) {
		t.Fatal("setup: a should have forgotten the writer, first not yet")
	}
	removal := del(t, a.r, "/k")
	first.mergeBlock(a, removal)
	late.mergeBlock(a, removal)
	late.mergeBlock(w, add)
	if !late.knows(w.r.replica) {
		t.Fatal("setup: late should merge the put, its drop clock short of the expiry")
	}
	for _, sr := range []*simReplica{first, late} {
		sr.wantValue(t, "/k", "<deleted>")
		if n := countEntries(t, sr.r, sr.r.set.pendingPrefix()); n != 1 {
			t.Fatalf("%s keeps %d pending removals of /k, want 1", sr, n)
		}
	}
	first.r.mu.Lock()
	want := *first.r.cc.inc[w.r.replica]
	first.r.mu.Unlock()
	late.r.mu.Lock()
	got := *late.r.cc.inc[w.r.replica]
	late.r.mu.Unlock()
	if got.changed != want.changed || got.expires != want.expires || !slices.Equal(got.spans, want.spans) {
		t.Fatalf("late holds the writer as %+v from the same blocks first holds as %+v", got, want)
	}
	st.advance(simS)
	st.heartbeat(t, first, late)
	first.maintain(t)
	late.maintain(t)
	if first.knows(w.r.replica) || late.knows(w.r.replica) {
		t.Fatal("the removal kept alive an entry that had expired before it was written")
	}
}

func TestARemovalTouchesItsEntryWhateverOrderItsIncarnationsDeltasArrive(t *testing.T) {
	st := newSimTime()
	w, a := st.replica(t, "w", 0), st.replica(t, "a", 0)
	before, after := st.replica(t, "before", -simS/2), st.replica(t, "after", -simS/2)
	first := put(t, w.r, "/k", "v")
	st.advance(simMargin)
	second := put(t, w.r, "/j", "u")
	a.pull(t, w)
	before.mergeBlock(w, first)
	before.mergeBlock(w, second)
	after.mergeBlock(w, first)
	d := deltaAt(t, w, first)
	expires := d.GetAt() + d.GetTtl() + a.r.times.margin
	for a.r.dropClockNow() < expires {
		st.advance(min(simRI, time.Duration(expires-a.r.dropClockNow())*time.Millisecond))
		st.heartbeat(t, a, before, after)
		a.maintain(t)
		before.maintain(t)
		after.maintain(t)
	}
	if !after.knows(w.r.replica) {
		t.Fatal("setup: after should still know the writer, its drop clock short of the first put's expiry")
	}
	removal := del(t, a.r, "/k")
	before.mergeBlock(a, removal)
	after.mergeBlock(a, removal)
	after.mergeBlock(w, second)
	for _, sr := range []*simReplica{before, after} {
		sr.wantValue(t, "/k", "<deleted>")
		sr.wantValue(t, "/j", "u")
	}
	if b, f := countEntries(t, before.r, before.r.set.pendingPrefix()), countEntries(t, after.r, after.r.set.pendingPrefix()); b != f {
		t.Fatalf("after keeps %d pending removals from the blocks before keeps %d from", f, b)
	}
	before.r.mu.Lock()
	want := *before.r.cc.inc[w.r.replica]
	before.r.mu.Unlock()
	after.r.mu.Lock()
	got := *after.r.cc.inc[w.r.replica]
	after.r.mu.Unlock()
	if got.changed != want.changed || got.expires != want.expires || !slices.Equal(got.spans, want.spans) {
		t.Fatalf("after holds the writer as %+v from the blocks before holds it as %+v from", got, want)
	}
}
