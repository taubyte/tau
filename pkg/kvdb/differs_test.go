package kvdb

import (
	"context"
	"testing"
	"time"
)

// pendingOf is the one pending removal r holds.
func pendingOf(t testing.TB, r *Datastore) removal {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []removal
	err := scanStore(context.Background(), r.store, r.set.pendingPrefix(), func(k string, v []byte) error {
		p, err := r.set.parsePending(k, v)
		out = append(out, p)
		return err
	})
	if err != nil || len(out) != 1 {
		t.Fatalf("want one pending removal, have %d (%v)", len(out), err)
	}
	return out[0]
}

// refresh writes a snapshot of sr's state even when it holds nothing to fold,
// as maintenance does to replace an old one.
func (sr *simReplica) refresh(t testing.TB) {
	t.Helper()
	if _, err := sr.r.compact(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}

// untilDropClock advances until sr's drop clock reaches at, a rebroadcast
// at a time: the speakers beat and everyone hears them, the deaf are heard
// by nobody, and everyone maintains.
func (s *simTime) untilDropClock(t testing.TB, sr *simReplica, at uint64, speakers []*simReplica, deaf ...*simReplica) {
	t.Helper()
	all := append(append([]*simReplica(nil), speakers...), deaf...)
	for {
		now := sr.r.dropClockNow()
		if now >= at {
			return
		}
		s.advance(min(simRI, time.Duration(at-now)*time.Millisecond))
		for _, o := range speakers {
			o.beat()
		}
		for _, o := range speakers {
			var rest []*simReplica
			for _, x := range all {
				if x != o {
					rest = append(rest, x)
				}
			}
			s.deliver(t, o, rest...)
		}
		for _, o := range deaf {
			o.out.take()
		}
		for _, o := range all {
			o.maintain(t)
		}
	}
}

// TestAMergeThatLeavesADifferentStateIsCompacted (M4): a replica whose drop
// clock is behind still refuses an ancient element, by a pending removal the
// others have already forgotten, from the snapshot that becomes its only
// head. Once the removal expires there too, the state it compacts lacks the
// element, and so does every snapshot it merges: the replicas holding it
// must answer a snapshot that neither holds nor covers what they keep, or
// they read differently for good.
func TestAMergeThatLeavesADifferentStateIsCompacted(t *testing.T) {
	st := newSimTime()
	w := st.replica(t, "w", 0)
	a, b, c := st.replica(t, "a", -simS/2), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	st.away(t, simRetention+2*simMargin, a, b, c)
	if a.knows(w.r.replica) || c.knows(w.r.replica) {
		t.Fatal("setup: the writer should be forgotten, /k ancient")
	}

	del(t, a.r, "/k")
	compact(t, a.r)
	a.out.take()
	p := pendingOf(t, a.r)
	gone := p.expires() + c.r.times.margin
	st.untilDropClock(t, c, gone-uint64(simS/time.Millisecond), []*simReplica{b, c}, a)
	c.pull(t, a)
	c.wantValue(t, "/k", "<deleted>")
	st.untilDropClock(t, c, gone, []*simReplica{b}, a, c)
	if a.r.dropClockNow() >= p.expires()+a.r.times.margin {
		t.Fatal("setup: the removal should still be live by a's drop clock")
	}

	b.refresh(t)
	c.pull(t, b)
	c.wantValue(t, "/k", "v")
	c.refresh(t)
	a.pull(t, c)
	a.wantValue(t, "/k", "<deleted>")
	before := lastDot(a.r)
	compact(t, a.r)
	if lastDot(a.r) == before {
		t.Fatal("a refused an element of its only head and compacting folds nothing")
	}

	st.advance(simS)
	for range 3 {
		st.exchange(t, a, b, c)
		for _, sr := range []*simReplica{a, b, c} {
			sr.maintain(t)
		}
	}
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b} {
		if got, want := sr.value(t, "/k"), c.value(t, "/k"); got != want {
			t.Fatalf("after the removal expired everywhere, %s reads /k = %s and c reads %s", sr.name, got, want)
		}
	}
}
