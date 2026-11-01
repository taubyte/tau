package kvdb

import (
	"context"
	"testing"
	"time"
)

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

func (sr *simReplica) refresh(t testing.TB) {
	t.Helper()
	if _, err := sr.r.compact(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}

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
