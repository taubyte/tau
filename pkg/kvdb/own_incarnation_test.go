package kvdb

import "testing"

// TestACompactionAfterItsOwnEntryWentDrawsANewIncarnation (M5): a replica
// whose own blocks were all folded into a peer's snapshot under a longer T
// has nothing of its own to refresh, and forgets its own incarnation before
// it writes again. The compaction that forgetting asks for must not write
// under that incarnation: a peer still holding its old entry would extend it
// from the old counter, one that forgot it would open it afresh, and the two
// would cover different dots.
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
