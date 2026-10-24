package kvdb

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// hear hands what src has broadcast so far to each of dsts, which fetch from
// the given replicas only: they hear src, and may not be able to fetch what
// it holds.
func (s *simTime) hear(t testing.TB, src *simReplica, dsts []*simReplica, fetch ...*simReplica) {
	t.Helper()
	msgs := src.out.take()
	for _, dst := range dsts {
		dst.reach(fetch...)
		for _, data := range msgs {
			msg, err := dst.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			dst.r.handleBroadcast(context.Background(), msg)
		}
	}
}

// listen is one rebroadcast interval: the replicas in contact beat, hear and
// fetch from each other, answers included, and maintain; the listeners hear
// them and fetch nothing.
func (s *simTime) listen(t testing.TB, inContact []*simReplica, listeners ...*simReplica) {
	t.Helper()
	s.advance(simRI)
	for _, sr := range inContact {
		sr.beat()
	}
	for range 8 {
		quiet := true
		for _, src := range inContact {
			src.out.mu.Lock()
			pending := len(src.out.msgs) > 0
			src.out.mu.Unlock()
			if !pending {
				continue
			}
			quiet = false
			var others []*simReplica
			for _, o := range inContact {
				if o != src {
					others = append(others, o)
				}
			}
			msgs := src.out.take()
			for _, dst := range others {
				dst.reach(inContact...)
				for _, data := range msgs {
					msg, err := dst.r.decodeBroadcast(data)
					if err != nil {
						t.Fatal(err)
					}
					dst.r.handleBroadcast(context.Background(), msg)
				}
			}
			for _, dst := range listeners {
				dst.reach()
				for _, data := range msgs {
					msg, err := dst.r.decodeBroadcast(data)
					if err != nil {
						t.Fatal(err)
					}
					dst.r.handleBroadcast(context.Background(), msg)
				}
			}
		}
		if quiet {
			break
		}
	}
	for _, sr := range listeners {
		sr.out.take()
	}
	for _, sr := range inContact {
		sr.maintain(t)
	}
}

// settle keeps the replicas in contact, heartbeats only, until each holds
// all the others hold and knows it has heard them all.
func (s *simTime) settle(t testing.TB, rs ...*simReplica) {
	t.Helper()
	for range gapIntervals + 2 {
		s.advance(simRI)
		s.exchange(t, rs...)
	}
	for _, sr := range rs {
		if !sr.settled() {
			t.Fatalf("setup: %s is not settled after G in contact", sr.name)
		}
	}
}

func (sr *simReplica) settled() bool {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return sr.r.settled()
}

// TestAnUninformedJudgeDoesNotVouchForAnExpiringRefutation (residual A): a
// judge that hears the fleet and has not merged what it holds cannot see a
// removal the fleet holds that has expired and is not forgotten yet. It does
// not vouch for a replica that only that removal still refutes, and once the
// holders forget it the stale copy does not come back.
func TestAnUninformedJudgeDoesNotVouchForAnExpiringRefutation(t *testing.T) {
	st := newSimTime()
	st.advance(20 * simRI)
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "removed")
	st.settle(t, w, a, b, r)
	r.wantValue(t, "/x", "removed")
	// w is gone and r is cut off; a removes /x.
	st.advance(simRI)
	del(t, a.r, "/x")
	st.exchange(t, a, b)
	// The fleet runs until it holds the removal past its expiry, short of
	// forgetting it; j hears it for the last G of that, and none of its
	// blocks reaches j.
	st.away(t, simRetention+simMargin-3*simRI-(gapIntervals+1)*simRI, a, b)
	j := st.replica(t, "j", 0)
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b}, j)
	}

	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	st.deliver(t, r, j) // only j hears the rejoin
	st.deliver(t, j, r)
	if !r.rejoining() || r.rebases() != 0 {
		t.Fatalf("a judge that does not hold what the fleet holds vouched for r (rejoining=%t rebases=%d)", r.rejoining(), r.rebases())
	}

	// a and b forget the removal before anything of r reaches them.
	for range 4 {
		st.advance(simRI)
		st.exchange(t, a, b)
		a.maintain(t)
		b.maintain(t)
	}
	compact(t, r.r)
	st.exchange(t, a, b, r, j)
	st.exchange(t, a, b, r, j)
	for _, sr := range []*simReplica{a, b, r, j} {
		if got := sr.value(t, "/x"); got != "<deleted>" {
			t.Errorf("replica %s: /x = %s, want <deleted>", sr.name, got)
		}
	}
}

// TestPeersSettleAgainOnceAPeerHoldingWhatNobodyFetchedIsGone: a peer
// writes, announces it, and is gone before anyone fetched the block. While it
// is heard, what it said it holds counts and nobody vouches; once it has not
// been heard for G the others hold all that every live peer holds again, and
// a rejoin is answered well before the block expires.
func TestPeersSettleAgainOnceAPeerHoldingWhatNobodyFetchedIsGone(t *testing.T) {
	st := newSimTime()
	c, a, b, r := st.replica(t, "c", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, c, a, b, r)
	// r leaves, so it will rejoin; c writes and its heartbeat reaches a and
	// b, who cannot fetch the block; then c is gone.
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{c, a, b})
	}
	put(t, c.r, "/c", "1")
	st.advance(simRI)
	c.beat()
	st.hear(t, c, []*simReplica{a, b}, a, b)
	gone := st.ms()
	if a.settled() || b.settled() {
		t.Fatal("a peer that said it holds a block nobody could fetch did not count")
	}

	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	for r.rejoining() {
		if elapsed := time.Duration(st.ms()-gone) * time.Millisecond; elapsed > (gapIntervals+2)*simRI {
			t.Fatalf("a rejoin was still unanswered %s after the only holder of a block was gone", elapsed)
		}
		st.advance(simRI)
		st.exchange(t, a, b, r)
		a.maintain(t)
		b.maintain(t)
	}
	if !a.settled() || !b.settled() {
		t.Fatal("the peers did not settle again once the gone peer was not heard for G")
	}
	for _, sr := range []*simReplica{a, b, r} {
		sr.wantValue(t, "/k", "v")
		sr.wantValue(t, "/c", "<deleted>")
	}
}

// TestALivePeerThatCannotServeStillCounts: a peer that keeps heartbeating
// while nobody can fetch its blocks counts all the same. A removal only it
// holds is still refuting: no judge that does not hold it vouches for a
// replica it refutes, and the removed value does not come back.
func TestALivePeerThatCannotServeStillCounts(t *testing.T) {
	st := newSimTime()
	st.advance(20 * simRI)
	w, a, b, p, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "p", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "removed")
	// Long enough that r's gap is clear of the put: forgetting the put says
	// nothing about r then.
	st.settle(t, w, a, b, p, r)
	st.settle(t, w, a, b, p, r)
	// w is gone and r is cut off; p removes /x and nobody can fetch from p,
	// which keeps hearing a and b and being heard.
	st.advance(simRI)
	del(t, p.r, "/x")
	deaf := func() {
		st.advance(simRI)
		for _, sr := range []*simReplica{a, b, p} {
			sr.beat()
		}
		st.hear(t, a, []*simReplica{b, p}, a, b)
		st.hear(t, b, []*simReplica{a, p}, a, b)
		st.hear(t, p, []*simReplica{a, b}, a, b)
		for _, sr := range []*simReplica{a, b, p} {
			sr.maintain(t)
		}
	}
	for elapsed := time.Duration(0); elapsed < simRetention+simMargin-3*simRI; elapsed += simRI {
		deaf()
	}
	a.wantValue(t, "/x", "removed")

	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	st.hear(t, r, []*simReplica{a, b}, a, b)
	for _, j := range []*simReplica{a, b} {
		st.hear(t, j, []*simReplica{r}, a, b)
	}
	if !r.rejoining() || r.rebases() != 0 {
		t.Fatalf("a judge that does not hold what a live peer holds answered r (rejoining=%t rebases=%d)", r.rejoining(), r.rebases())
	}
	// p judges it too: it holds the removal, expired.
	r.beat()
	st.hear(t, r, []*simReplica{p}, a, b)
	st.hear(t, p, []*simReplica{r}, a, b)
	if r.rebases() != 1 {
		t.Fatal("the peer holding the expired removal did not rebase r")
	}
	// Once p serves, before it forgets the removal, nobody shows /x.
	st.exchange(t, a, b, p, r)
	st.exchange(t, a, b, p, r)
	for _, sr := range []*simReplica{a, b, p, r} {
		sr.wantValue(t, "/x", "<deleted>")
	}
}

// TestAnOkFollowedByAFailedFetchLeavesTheReplicaRejoining (A4): a replica
// vouched for advances its fleet time only once it holds what the judge
// vouched with. A fetch of the judge's heads that fails leaves it rejoining
// and reporting the time it had, until a later ok's state is fetched.
func TestAnOkFollowedByAFailedFetchLeavesTheReplicaRejoining(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b, r)
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b})
	}
	put(t, a.r, "/y", "1")
	st.exchange(t, a, b)
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	gap, since := r.r.reportedSinceNow(), r.since()

	st.hear(t, r, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{r}) // the ok, and nothing of a's to fetch
	if !r.rejoining() {
		t.Fatal("an ok ended the rejoin before the replica held what the judge vouched with")
	}
	if r.r.reportedSinceNow() != gap || r.since() != since {
		t.Fatalf("an ok whose state was not fetched moved the fleet time: reports %d, since %d, had %d and %d", r.r.reportedSinceNow(), r.since(), gap, since)
	}
	r.wantValue(t, "/y", "<deleted>")

	st.advance(simRI)
	r.beat()
	st.hear(t, r, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{r}, a, b)
	if r.rejoining() {
		t.Fatal("an ok whose state was fetched did not end the rejoin")
	}
	r.wantValue(t, "/y", "1")
	if r.since() <= since {
		t.Fatal("the end of the rejoin did not take the judge's fleet time")
	}
}

func (sr *simReplica) since() uint64 {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return sr.r.since
}

// TestAJudgeThatVanishesAfterAnOkLeavesTheRejoinToTheNext: a judge vouches,
// then is never heard again, and what only it held cannot be fetched. The
// replica stays rejoining with the time it had, drops that ok once the judge
// is not heard for G, and ends its rejoin on the next judge's ok; nothing
// only the vanished judge held is needed for that, and no peer waits for the
// vanished judge for longer than G.
func TestAJudgeThatVanishesAfterAnOkLeavesTheRejoinToTheNext(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, b.r, "/k", "v")
	st.settle(t, a, b, r)
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b})
	}
	// a writes what nobody fetches, and its heartbeat reaches b.
	put(t, a.r, "/only", "a")
	st.advance(simRI)
	a.beat()
	st.hear(t, a, []*simReplica{b}, b)
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	gap := r.r.reportedSinceNow()
	st.hear(t, r, []*simReplica{a, b}, b)
	st.hear(t, b, []*simReplica{r}, b)
	if !r.rejoining() {
		t.Fatal("setup: b does not hold what a holds and should not vouch yet")
	}
	st.hear(t, a, []*simReplica{r}, b) // a's ok; then a is never heard again
	if !r.rejoining() || r.r.reportedSinceNow() != gap {
		t.Fatal("an ok whose judge's state cannot be fetched ended the rejoin")
	}
	vanished, judge := st.ms(), a.incarnation()

	for r.rejoining() {
		if elapsed := time.Duration(st.ms()-vanished) * time.Millisecond; elapsed > (gapIntervals+2)*simRI {
			t.Fatalf("r was still rejoining %s after its judge vanished", elapsed)
		}
		st.advance(simRI)
		b.beat()
		r.beat()
		st.hear(t, b, []*simReplica{r}, b)
		st.hear(t, r, []*simReplica{b}, b)
		st.hear(t, b, []*simReplica{r}, b)
		b.maintain(t)
		r.r.mu.Lock()
		_, held := r.r.oks[judge]
		r.r.mu.Unlock()
		if held && time.Duration(st.ms()-vanished)*time.Millisecond > gapIntervals*simRI+simRI {
			t.Fatal("r keeps the ok of a judge not heard for G")
		}
	}
	r.wantValue(t, "/k", "v")
	r.wantValue(t, "/only", "<deleted>")
}

// TestAJudgeThatVanishesAfterARebaseLeavesNothingWaiting: a judge tells a
// stale replica to rebase and is gone before any of its blocks is fetched.
// The rebase completes all the same: the replica keeps what it wrote, takes
// what the live peers hold, and holds all they hold within G.
func TestAJudgeThatVanishesAfterARebaseLeavesNothingWaiting(t *testing.T) {
	st := newSimTime()
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "v")
	st.settle(t, w, a, b, r)
	del(t, a.r, "/x")
	put(t, r.r, "/p", "mine")
	st.away(t, simRetention+2*simMargin, a, b)
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	before := r.incarnation()
	st.hear(t, r, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{r}) // the rebase; then a is never heard again
	if r.rejoining() || r.incarnation() == before {
		t.Fatal("setup: a should have rebased r")
	}
	r.r.mu.Lock()
	settledSince := r.r.settledSince
	r.r.mu.Unlock()
	if settledSince != 0 {
		t.Fatal("a rebase kept the time the replica last held what its peers held")
	}
	rebased := st.ms()
	for !r.settled() {
		if elapsed := time.Duration(st.ms()-rebased) * time.Millisecond; elapsed > (gapIntervals+2)*simRI {
			t.Fatalf("r did not hold what the live fleet holds %s after a rebase on a judge that vanished", elapsed)
		}
		st.advance(simRI)
		st.exchange(t, b, r)
		b.maintain(t)
		r.maintain(t)
	}
	for _, sr := range []*simReplica{b, r} {
		sr.wantValue(t, "/x", "<deleted>")
		sr.wantValue(t, "/p", "mine")
	}
}

// TestAJudgeThatHeardNoFrontierDoesNotVouch: a replica that hears the fleet
// only through the heads announced as they are written, and none of its
// heartbeats, knows the fleet is there and not what it holds: it does not
// vouch.
func TestAJudgeThatHeardNoFrontierDoesNotVouch(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b, r)
	j := st.replica(t, "j", 0)
	for i := range gapIntervals + 2 {
		put(t, a.r, "/w", fmt.Sprint(i))
		st.hear(t, a, []*simReplica{b, j}, a, b)
		st.advance(simRI)
		st.exchange(t, a, b)
	}
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	st.hear(t, r, []*simReplica{j}, a, b)
	st.hear(t, j, []*simReplica{r}, a, b)
	if !r.rejoining() || r.rebases() != 0 {
		t.Fatalf("a judge that heard no heartbeat answered r (rejoining=%t rebases=%d)", r.rejoining(), r.rebases())
	}
}

// expiringRemoval is a fleet a, b that holds a removal of r's /x expired and
// not forgotten yet, r cut off since before it: an informed judge rebases r.
func expiringRemoval(t *testing.T, st *simTime) (a, b, r *simReplica) {
	t.Helper()
	st.advance(20 * simRI)
	w := st.replica(t, "w", 0)
	a, b, r = st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "removed")
	st.settle(t, w, a, b, r)
	st.advance(simRI)
	del(t, a.r, "/x")
	st.exchange(t, a, b)
	st.away(t, simRetention+simMargin-3*simRI-(gapIntervals+1)*simRI, a, b)
	return a, b, r
}

// TestANewJudgeDoesNotVouchUntilItKnowsItHeardTheFleet: a replica that has
// just joined and holds all that the one peer it heard holds cannot tell
// whether another peer holds more: that peer may have just joined too. It
// vouches only once it has heard the fleet for G, or holds what a peer that
// was settled holds.
func TestANewJudgeDoesNotVouchUntilItKnowsItHeardTheFleet(t *testing.T) {
	st := newSimTime()
	a, b, r := expiringRemoval(t, st)
	// q has heard a and holds none of what it holds; j hears only q.
	q, j := st.replica(t, "q", 0), st.replica(t, "j", 0)
	a.beat()
	st.hear(t, a, []*simReplica{q})
	q.beat()
	st.hear(t, q, []*simReplica{j}, q)
	if j.settled() {
		t.Fatal("a judge that has heard one peer that was not settled is settled")
	}
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	st.hear(t, r, []*simReplica{j}, q)
	st.hear(t, j, []*simReplica{r}, q)
	if !r.rejoining() || r.rebases() != 0 {
		t.Fatalf("a judge that cannot know it heard the fleet answered r (rejoining=%t rebases=%d)", r.rejoining(), r.rebases())
	}
	// Once it holds what a settled peer holds, it sees the expired removal.
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b, q, j})
	}
	r.beat()
	st.hear(t, r, []*simReplica{j}, a, b)
	st.hear(t, j, []*simReplica{r}, a, b)
	if r.rebases() != 1 {
		t.Fatal("a judge holding what the fleet holds did not rebase r")
	}
}

// TestAJudgeHoldingWhatASettledPeerHoldsVouchesAtOnce: a replica that has
// just joined and holds all that settled peers hold knows it holds what the
// fleet holds, and vouches without waiting G.
func TestAJudgeHoldingWhatASettledPeerHoldsVouchesAtOnce(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b, r)
	r.reopen(t)
	j := st.replica(t, "j", 0)
	a.beat()
	b.beat()
	st.hear(t, a, []*simReplica{b, j}, a, b)
	st.hear(t, b, []*simReplica{a, j}, a, b)
	if !j.settled() {
		t.Fatal("a replica holding what settled peers hold is not settled")
	}
	r.beat()
	st.hear(t, r, []*simReplica{j}, a, b)
	st.hear(t, j, []*simReplica{r}, a, b, j)
	if r.rejoining() {
		t.Fatal("a judge holding what settled peers hold did not vouch")
	}
}

// TestAReplicaThatNeverHeldWhatItsPeersHeldRejoinsFromNoTime: a replica
// that heard the fleet's time without ever holding what the fleet held
// rejoins from no time at all: as a judge it vouches for nobody who held
// more, and it is judged as one that may hold anything.
func TestAReplicaThatNeverHeldWhatItsPeersHeldRejoinsFromNoTime(t *testing.T) {
	st := newSimTime()
	a, b, r := expiringRemoval(t, st)
	j := st.replica(t, "j", 0)
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b}, j)
	}
	// j and r are cut off from the fleet, and only hear each other.
	st.advance((gapIntervals + 1) * simRI)
	j.beat()
	r.beat()
	if !j.rejoining() || !r.rejoining() {
		t.Fatal("setup: j and r should rejoin")
	}
	if got := j.r.reportedSinceNow(); got != 1 {
		t.Fatalf("a replica that never held what its peers held rejoins from %d", got)
	}
	j.out.take()
	st.hear(t, r, []*simReplica{j}, j, r)
	st.hear(t, j, []*simReplica{r}, j, r)
	if !r.rejoining() || r.rebases() != 0 {
		t.Fatalf("a judge that never held what the fleet held answered r (rejoining=%t rebases=%d)", r.rejoining(), r.rebases())
	}
	a.wantValue(t, "/x", "<deleted>")
	b.wantValue(t, "/x", "<deleted>")
}

// TestASettledReplicaRejoinsFromTheTimeItLastHeldAfterAClose: the time a
// replica last held all its live peers held is kept across a close: open
// again, it rejoins from it.
func TestASettledReplicaRejoinsFromTheTimeItLastHeldAfterAClose(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b)
	b.beat() // records the time it holds what a holds
	b.r.mu.Lock()
	held := b.r.settledSince
	b.r.mu.Unlock()
	if held <= 1 {
		t.Fatal("setup: b should be settled")
	}
	b.reopen(t)
	if got := b.r.reportedSinceNow(); !b.rejoining() || got != held {
		t.Fatalf("reopened, b rejoins from %d (rejoining=%t), want %d", got, b.rejoining(), held)
	}
}

// TestAJudgeIsSettledWhenAPeerSendsWhatItHasForgotten: a peer that has not
// forgotten an entry yet sends it; a judge that never held it, and whose
// horizon is past it, holds it as well as anyone can.
func TestAJudgeIsSettledWhenAPeerSendsWhatItHasForgotten(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	start := st.ms()
	put(t, w.r, "/x", "v")
	st.settle(t, w, a, b)
	for st.ms() < start+durationMillis(simRetention+simMargin) {
		st.advance(simRI)
		st.exchange(t, a, b)
	}
	// b forgets w's entry; a, which fetches nothing more, keeps it.
	b.maintain(t)
	j := st.replica(t, "j", 0)
	a.beat()
	st.hear(t, a, []*simReplica{b, j}, b)
	b.beat()
	st.hear(t, b, []*simReplica{j}, b)
	b.beat()
	st.hear(t, b, []*simReplica{a}) // a fetches nothing
	if !a.knows(w.incarnation()) || b.knows(w.incarnation()) || j.knows(w.incarnation()) {
		t.Fatal("setup: a should hold w's entry, b should have forgotten it and j never held it")
	}
	if !j.settled() {
		t.Fatal("a judge whose horizon is past an entry a peer still sends is not settled")
	}
}

// TestAJudgeIsSettledWhenAPeerTouchesWhatItForgot: a removal a peer makes
// before it forgets an entry keeps that entry there for the removal's
// lifetime, while a judge that forgot it first holds the removal as a pending
// one. The judge held all of the entry when it dropped it, and keeps that
// record while a peer sends the entry: it holds it as well as anyone can.
func TestAJudgeIsSettledWhenAPeerTouchesWhatItForgot(t *testing.T) {
	st := newSimTime()
	w, a, j := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "j", 0)
	start := st.ms()
	put(t, w.r, "/e", "v")
	st.settle(t, w, a, j)
	for st.ms() < start+durationMillis(simRetention+simMargin) {
		st.advance(simRI)
		st.exchange(t, a, j)
	}
	// j forgets w's entry; a, which fetches nothing for a while, keeps it.
	j.maintain(t)
	keep := st.opts().DeliveryBound + st.opts().ClockSkew + gapIntervals*simRI
	for elapsed := time.Duration(0); elapsed <= keep+simRI; elapsed += simRI {
		st.advance(simRI)
		a.beat()
		j.beat()
		st.hear(t, a, []*simReplica{j}, a)
		st.hear(t, j, []*simReplica{a})
	}
	if !a.knows(w.incarnation()) || j.knows(w.incarnation()) {
		t.Fatal("setup: a should hold w's entry and j should have forgotten it")
	}
	del(t, a.r, "/e")
	a.beat()
	st.hear(t, a, []*simReplica{j}, a)
	j.wantValue(t, "/e", "<deleted>")
	if !j.settled() {
		t.Fatal("a judge that held all of an entry when it forgot it is not settled while a peer still sends it")
	}
}

func (s *simTime) opts() *Options { return s.options(0) }

// TestAJudgeDoesNotWaitForWhatAStaleSenderHolds: what a replica told to
// rebase says it holds is not walked, so it is not waited for: the judge
// stays settled and vouches for others.
func TestAJudgeDoesNotWaitForWhatAStaleSenderHolds(t *testing.T) {
	st := newSimTime()
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "v")
	st.settle(t, w, a, b, r)
	start := st.ms()
	st.away(t, simRetention+2*simMargin, a, b, r)
	// s says it last heard the fleet when /x was written, holding what
	// nobody else does.
	s := st.replica(t, "s", 0)
	put(t, s.r, "/s", "v")
	s.r.mu.Lock()
	s.r.since = start
	s.r.mu.Unlock()
	s.beat()
	st.hear(t, s, []*simReplica{a}, a, b, s)
	if !a.settled() {
		t.Fatal("a judge waits for what a sender it told to rebase holds")
	}
	r.reopen(t)
	r.beat()
	st.hear(t, r, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{r}, a, b)
	if r.rejoining() {
		t.Fatal("a judge that told a stale sender to rebase did not vouch for a fresh one")
	}
}

// TestAReplicaHearingOnlyRejoiningPeersVouchesForThem: once its rejoin ends,
// a replica may hear no peer that is not rejoining. It vouches for them as of
// the time it holds, as a rejoining judge does, so the rejoins end rather
// than pass from one replica to the other.
func TestAReplicaHearingOnlyRejoiningPeersVouchesForThem(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b)
	a.reopen(t)
	b.reopen(t)
	a.beat()
	st.hear(t, a, []*simReplica{b}, a, b)
	st.hear(t, b, []*simReplica{a}, a, b)
	if a.rejoining() || !b.rejoining() {
		t.Fatalf("setup: b should have vouched for a (a rejoining=%t, b rejoining=%t)", a.rejoining(), b.rejoining())
	}
	b.beat()
	st.hear(t, b, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{b}, a, b)
	if b.rejoining() {
		t.Fatal("a replica hearing only rejoining peers did not vouch for them")
	}
}
