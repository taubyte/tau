package kvdb

import "testing"

func TestACompactionAfterItsOwnEntryWentDrawsANewIncarnation(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	b.setRetention(t, 3*simRetention)
	put(t, a.r, "/k", "v")
	inc := a.r.replica
	st.exchange(t, a, b)
	compact(t, b.r)
	st.exchange(t, a, b)
	st.away(t, simRetention+2*simMargin, a, b)
	a.r.mu.Lock()
	now := a.r.replica
	a.r.mu.Unlock()
	if now == inc {
		t.Fatalf("a compacted under its own incarnation after forgetting it (still knows it: %t)", a.knows(inc))
	}
	if !a.knows(now) {
		t.Fatal("a's new incarnation is not in its context")
	}
	a.wantValue(t, "/k", "v")
	b.wantValue(t, "/k", "v")
}
