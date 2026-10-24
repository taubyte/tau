package kvdb

import (
	"context"
	"testing"
	"time"
)

func (sr *simReplica) rejoining() bool {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return sr.r.rejoining
}

func (sr *simReplica) incarnation() string { return sr.r.currentIncarnation() }

// away keeps replicas in contact for d of virtual time, a heartbeat round
// and a maintenance pass every rebroadcast interval, while the others hear
// nothing.
func (s *simTime) away(t testing.TB, d time.Duration, inContact ...*simReplica) {
	t.Helper()
	for elapsed := time.Duration(0); elapsed < d; elapsed += simRI {
		s.advance(simRI)
		s.exchange(t, inContact...)
		for _, sr := range inContact {
			sr.maintain(t)
		}
	}
}

// TestAReopenedReplicaRejoinsAndIsVouchedFor: a replica that heard a fleet
// and opens again cannot tell how long it was closed. It rejoins: nobody
// walks its heads until a peer that can still refute what it might carry
// says so.
func TestAReopenedReplicaRejoinsAndIsVouchedFor(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "v")
	st.exchange(t, a, b)
	b.wantValue(t, "/k", "v")

	b.reopen(t)
	if !b.rejoining() {
		t.Fatal("a replica that heard a fleet opened without rejoining")
	}
	put(t, b.r, "/y", "1") // writes never wait
	b.beat()
	st.deliver(t, b, a)
	a.wantValue(t, "/y", "<deleted>") // nobody walks a rejoining replica
	st.deliver(t, a, b)               // the ok
	if b.rejoining() {
		t.Fatal("an ok from a peer did not end the rejoin")
	}
	st.exchange(t, a, b)
	a.wantValue(t, "/y", "1")
}

// TestAStaleReplicaIsRebasedAndKeepsWhatItWrote (T8.7): a replica away for
// longer than the fleet keeps removals is rebased on a judge's state, never
// shows what the fleet removed meanwhile, and writes again what it wrote
// while away.
func TestAStaleReplicaIsRebasedAndKeepsWhatItWrote(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	c.wantValue(t, "/k", "v")
	before := c.incarnation()

	// w is gone and c is cut off. The fleet removes /k; c writes /p and
	// keeps /k. Once /k's writer is forgotten, nothing refutes c's copy.
	del(t, a.r, "/k")
	put(t, c.r, "/p", "mine")
	st.away(t, simRetention+2*simMargin, a, b)
	c.beat() // c has heard nobody for longer than G
	if !c.rejoining() {
		t.Fatal("a replica that heard nobody for longer than G is not rejoining")
	}

	st.exchange(t, a, b, c)
	if c.rejoining() || c.incarnation() == before {
		t.Fatal("the stale replica was not rebased")
	}
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/k", "<deleted>")
		sr.wantValue(t, "/p", "mine")
	}
	if has, _ := c.store.Has(context.Background(), c.r.rebaseKey()); has {
		t.Fatal("the rebase is still marked after its writes were reissued")
	}
}

// TestARejoiningReplicaReportsItsGap (T8.4): a stale replica that hears a
// heartbeat before it is judged does not take that heartbeat's time: it
// reports the time it had before its gap, and is judged by it.
func TestARejoiningReplicaReportsItsGap(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	del(t, a.r, "/k")
	st.away(t, simRetention+2*simMargin, a, b)
	c.beat()
	gap := c.r.reportedSinceNow()

	a.beat()
	st.deliver(t, a, c) // c hears a's fresh heartbeat first
	if got := c.r.reportedSinceNow(); got != gap {
		t.Fatalf("a rejoining replica reports %d after hearing a heartbeat, want its gap_from %d", got, gap)
	}
	st.exchange(t, a, b, c)
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/k", "<deleted>")
	}
}

func (store *Datastore) reportedSinceNow() uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.reportedSince()
}

// TestALateVerdictIsDeadOnArrival (T8.9): a rebase draws a new incarnation,
// so a second verdict for the old one, arriving late, changes nothing.
func TestALateVerdictIsDeadOnArrival(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	del(t, a.r, "/k")
	st.away(t, simRetention+2*simMargin, a, b)

	c.beat()
	st.deliver(t, c, a, b) // both judges answer
	first := a.out.take()
	second := b.out.take()
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("setup: both judges should have answered")
	}
	a.out.msgs = first
	before := c.incarnation()
	st.deliver(t, a, c) // c rebases on a
	rebased := c.incarnation()
	if rebased == before {
		t.Fatal("setup: the first verdict should have rebased c")
	}
	put(t, c.r, "/after", "1")

	b.out.msgs = second
	st.deliver(t, b, c) // the late verdict, for the old incarnation
	if c.incarnation() != rebased {
		t.Fatal("a verdict for an incarnation that is gone was acted on")
	}
	c.wantValue(t, "/after", "1")
}

// TestAVerdictFromAJudgeBehindIsIgnored (T8.8): a judge whose clock is behind
// the replica's may still hold what the fleet has forgotten and vouch for a
// stale replica; its verdict is not taken. Nor is one from a judge that has
// heard an older fleet than the replica had.
func TestAVerdictFromAJudgeBehindIsIgnored(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	del(t, a.r, "/k")
	st.away(t, simRetention+2*simMargin, a, b)
	c.beat()

	slow := st.replica(t, "slow", -10*simRetention)
	slow.r.mu.Lock()
	slow.r.since = c.r.reportedSinceNow() // as if it had heard as much as c
	slow.r.mu.Unlock()
	st.deliver(t, c, slow)
	st.deliver(t, slow, c)
	if !c.rejoining() {
		t.Fatal("a stale replica took an ok from a judge whose clock is behind")
	}
	c.wantValue(t, "/k", "v") // still its own stale state, walked by nobody

	st.exchange(t, a, b, c)
	st.exchange(t, a, b, c)
	c.wantValue(t, "/k", "<deleted>")
	a.wantValue(t, "/k", "<deleted>")
}

// TestAReplicaThatHearsPeersDoesNotRejoin: G of silence starts a rejoin;
// heartbeats keep it from starting, writes or not.
func TestAReplicaThatHearsPeersDoesNotRejoin(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "v")
	st.exchange(t, a, b)
	for range 10 {
		st.advance(simRI)
		st.exchange(t, a, b)
	}
	if a.rejoining() || b.rejoining() {
		t.Fatal("a replica hearing heartbeats started a rejoin")
	}
	st.advance(gapIntervals*simRI + time.Millisecond)
	b.beat()
	if !b.rejoining() {
		t.Fatal("a replica that heard nobody for G did not rejoin")
	}
}

// TestAQuietFleetVouchesForAReplicaThatWasNeverAway: heartbeats carry the
// fleet's time even with no heads, so after a long quiet spell in contact a
// replica that restarts is judged by a recent time and is not rebased.
func TestAQuietFleetVouchesForAReplicaThatWasNeverAway(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b)
	del(t, a.r, "/k")
	st.exchange(t, w, a, b)
	st.away(t, simRetention+2*simMargin, w, a, b)

	before := b.incarnation()
	b.reopen(t)
	reopened := b.incarnation()
	st.exchange(t, w, a, b)
	if b.rejoining() {
		t.Fatal("the reopened replica was not judged")
	}
	if b.incarnation() != reopened {
		t.Fatalf("a replica that was never away was rebased (incarnation %x -> %x -> %x)", before, reopened, b.incarnation())
	}
}

// TestAPeersAcksForgetTheOwnOperations: what a peer's heartbeat shows it has
// merged is no longer kept for a rebase.
func TestAPeersAcksForgetTheOwnOperations(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "1")
	st.exchange(t, a, b)
	put(t, a.r, "/k", "2")
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 1 {
		t.Fatalf("a keeps %d own operations, want the one nobody acknowledged", n)
	}
	st.exchange(t, a, b)
	st.exchange(t, a, b)
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 0 {
		t.Fatalf("a keeps %d own operations its peer acknowledged", n)
	}
}

// TestARebaseInterruptedByACrashFinishesAtOpen: the own operations a rebase
// keeps survive a crash before they are written again, and the next open
// writes them.
func TestARebaseInterruptedByACrashFinishesAtOpen(t *testing.T) {
	st := newSimTime()
	a, c := st.replica(t, "a", 0), st.replica(t, "c", 0)
	put(t, a.r, "/k", "v")
	st.exchange(t, a, c)
	put(t, c.r, "/p", "mine")
	ctx := context.Background()

	c.r.mu.Lock()
	if err := c.r.dropAllButOwn(ctx, st.ms()); err != nil {
		t.Fatal(err)
	}
	c.r.mu.Unlock()
	// The crash: the store as the rebase left it, reopened.
	c.reopen(t)
	c.wantValue(t, "/p", "mine")
	if has, _ := c.store.Has(ctx, c.r.rebaseKey()); has {
		t.Fatal("the rebase is still marked after the open wrote its operations")
	}
	st.exchange(t, a, c)
	st.exchange(t, a, c)
	a.wantValue(t, "/p", "mine")
	c.wantValue(t, "/k", "v")
}

// TestAFleetThatRestartsTogetherRejoins: when every replica opens again at
// once there is no live peer; the rejoining ones judge each other, and the
// one that heard the most recent fleet vouches for the rest.
func TestAFleetThatRestartsTogetherRejoins(t *testing.T) {
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, a.r, "/k", "v")
	st.exchange(t, a, b, c)
	st.advance(time.Hour)
	for _, sr := range []*simReplica{a, b, c} {
		sr.reopen(t)
		if !sr.rejoining() {
			t.Fatal("setup: every replica should rejoin")
		}
	}
	for range 3 {
		st.exchange(t, a, b, c)
	}
	for _, sr := range []*simReplica{a, b, c} {
		if sr.rejoining() {
			t.Fatalf("%s is still rejoining after a fleet restart", sr.name)
		}
		sr.wantValue(t, "/k", "v")
	}
	put(t, b.r, "/after", "1")
	st.exchange(t, a, b, c)
	a.wantValue(t, "/after", "1")
}
