package kvdb

import (
	"testing"
	"time"
)

// Changing T, and replicas reading different T for a while. Every block
// carries the T its author held, so what a block teaches expires at the time
// it says whoever reads it and whatever T the reader holds now; a judge
// vouches only for what it still holds and has not forgotten. These tests
// change T on some replicas and not others, in both directions, around a
// replica that is away, and check nothing removed comes back and every
// replica agrees.

func (sr *simReplica) setRetention(t testing.TB, d time.Duration) {
	t.Helper()
	if err := sr.r.SetRetention(d); err != nil {
		t.Fatal(err)
	}
}

func (sr *simReplica) rebases() int {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return int(sr.r.generation)
}

// TestLoweringTForgetsNothingWrittenBefore: a removal written under a long T
// is kept that long after T is lowered, so a replica away for longer than the
// new T but not the old one rejoins without being rebased, and what it holds
// stays removed.
func TestLoweringTForgetsNothingWrittenBefore(t *testing.T) {
	st := newSimTime()
	long := 4 * simRetention
	var rs []*simReplica
	for _, name := range []string{"w", "a", "b", "c"} {
		sr := st.replica(t, name, 0)
		sr.setRetention(t, long)
		rs = append(rs, sr)
	}
	w, a, b, c := rs[0], rs[1], rs[2], rs[3]
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	del(t, a.r, "/k") // written under the long T; c never hears it
	st.exchange(t, w, a, b)

	a.setRetention(t, simRetention)
	b.setRetention(t, simRetention)
	st.away(t, simRetention+3*simMargin, a, b) // longer than the new T, shorter than the old

	c.beat()
	st.exchange(t, a, b, c)
	st.exchange(t, a, b, c)
	if c.rejoining() || c.rebases() != 0 {
		t.Fatalf("c, away for less than the T its missed removal was written under, was rebased (%d) or never judged", c.rebases())
	}
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/k", "<deleted>")
	}
	assertSameState(t, []string{"a", "b", "c"}, a.r, b.r, c.r)
}

// TestRaisingTKeepsWhatWasWrittenOnlyAsLongAsItSays: raising T does not
// stretch what was written before, so a replica away for longer than the old
// T is judged by it and rebased, and nothing comes back.
func TestRaisingTKeepsWhatWasWrittenOnlyAsLongAsItSays(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	del(t, a.r, "/k") // written under the short T
	st.exchange(t, w, a, b)
	for _, sr := range []*simReplica{a, b} {
		sr.setRetention(t, 4*simRetention)
	}
	st.away(t, simRetention+3*simMargin, a, b)

	c.beat()
	st.exchange(t, a, b, c)
	st.exchange(t, a, b, c)
	if c.rebases() == 0 {
		t.Fatal("c missed a removal that has expired by the T it was written under, and was not rebased")
	}
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/k", "<deleted>")
	}
	assertSameState(t, []string{"a", "b", "c"}, a.r, b.r, c.r)
}

// TestReplicasOnDifferentTAgree: two replicas that read different T write
// under their own, and keep and forget everything at the same moments: the
// expiry of each thing is the one written in it.
func TestReplicasOnDifferentTAgree(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	b.setRetention(t, 3*simRetention)
	put(t, a.r, "/x", "1")
	put(t, b.r, "/y", "1")
	st.exchange(t, a, b)
	del(t, a.r, "/y")
	del(t, b.r, "/x")
	st.exchange(t, a, b)
	for range 4 {
		st.away(t, simRetention, a, b)
		st.exchange(t, a, b)
		assertSameState(t, []string{"a", "b"}, a.r, b.r)
	}
	a.wantValue(t, "/x", "<deleted>")
	a.wantValue(t, "/y", "<deleted>")
}
