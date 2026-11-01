package kvdb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

// The dsts fetch only from fetch: they hear src and may be unable to fetch
// what it holds.
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

// listen is one rebroadcast interval: inContact beat, hear, fetch from each
// other and maintain; listeners hear them and fetch nothing.
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

// settle exchanges heartbeats until each replica holds all the others hold and
// knows it heard them all.
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
	st.deliver(t, r, j)
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
	st.hear(t, a, []*simReplica{r})
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
	st.hear(t, a, []*simReplica{r}, b)
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
		r.r.mu.Lock()
		_, held := r.r.oks[judge]
		r.r.mu.Unlock()
		if held && time.Duration(st.ms()-vanished)*time.Millisecond > gapIntervals*simRI {
			t.Fatal("r keeps the ok of a judge not heard for G")
		}
		st.hear(t, b, []*simReplica{r}, b)
		st.hear(t, r, []*simReplica{b}, b)
		st.hear(t, b, []*simReplica{r}, b)
		b.maintain(t)
	}
	r.wantValue(t, "/k", "v")
	r.wantValue(t, "/only", "<deleted>")
}

func TestAnotherJudgesOkEndsTheRejoinWhileTheFirstStands(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, b.r, "/k", "v")
	st.settle(t, a, b, r)
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b})
	}
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	// a writes, after its last heartbeat, what nobody fetches, and vouches.
	put(t, a.r, "/only", "a")
	st.hear(t, r, []*simReplica{a, b}, b)
	st.hear(t, a, []*simReplica{r}, b)
	if !r.rejoining() {
		t.Fatal("setup: an ok whose judge's state cannot be fetched ended the rejoin")
	}
	st.hear(t, b, []*simReplica{r}, b)
	if r.rejoining() {
		t.Fatal("r still rejoins after an ok it holds, while another judge's stands")
	}
	r.wantValue(t, "/k", "v")
}

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
	// b's ok, whose state r cannot fetch, is held when the rebase lands.
	b.r.mu.Lock()
	ok := b.r.message(nil, &verdict{to: before, since: b.r.since, held: b.r.since})
	b.r.mu.Unlock()
	data, err := proto.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	b.out.msgs = [][]byte{data}
	st.hear(t, b, []*simReplica{r})
	st.hear(t, r, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{r})
	if r.rejoining() || r.incarnation() == before {
		t.Fatal("setup: a should have rebased r")
	}
	r.r.mu.Lock()
	settledSince, oks := r.r.settledSince, len(r.r.oks)
	r.r.mu.Unlock()
	if settledSince != 0 {
		t.Fatal("a rebase kept the time the replica last held what its peers held")
	}
	if oks != 0 {
		t.Fatal("an ok for the incarnation a rebase replaced outlived it")
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

// expiringRemoval is a fleet a, b holding a removal of r's /x expired and not
// forgotten yet, with r cut off since before it: an informed judge rebases r.
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

func TestASettledReplicaRejoinsFromTheTimeItLastHeldAfterAClose(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b)
	b.beat()
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
	st.hear(t, b, []*simReplica{a})
	if !a.knows(w.incarnation()) || b.knows(w.incarnation()) || j.knows(w.incarnation()) {
		t.Fatal("setup: a should hold w's entry, b should have forgotten it and j never held it")
	}
	if !j.settled() {
		t.Fatal("a judge whose horizon is past an entry a peer still sends is not settled")
	}
}

func TestAJudgeIsSettledWhenAPeerTouchesWhatItForgot(t *testing.T) {
	st := newSimTime()
	w, a, j := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "j", 0)
	start := st.ms()
	put(t, w.r, "/e", "v")
	st.settle(t, w, a, j)
	for st.ms() < start+durationMillis(simRetention) {
		st.advance(simRI)
		st.exchange(t, a, j)
	}
	// a removes /e before w's entry expires, which touches it there; j hears
	// a and fetches nothing until it has forgotten the entry.
	del(t, a.r, "/e")
	for st.ms() < start+durationMillis(simRetention+simMargin) {
		st.advance(simRI)
		a.beat()
		j.beat()
		st.hear(t, a, []*simReplica{j})
		st.hear(t, j, []*simReplica{a}, j)
	}
	j.maintain(t)
	if !a.knows(w.incarnation()) || j.knows(w.incarnation()) {
		t.Fatal("setup: a should hold w's entry and j should have forgotten it")
	}
	keep := st.opts().DeliveryBound + st.opts().ClockSkew + gapIntervals*simRI
	for elapsed := time.Duration(0); elapsed <= keep+simRI; elapsed += simRI {
		st.advance(simRI)
		a.beat()
		j.beat()
		st.hear(t, a, []*simReplica{j}, a)
		st.hear(t, j, []*simReplica{a})
	}
	j.wantValue(t, "/e", "<deleted>")
	if !j.settled() {
		t.Fatal("a judge that held all of an entry when it forgot it is not settled while a peer still sends it")
	}
}

func (s *simTime) opts() *Options { return s.options(0) }

func TestAJudgeDoesNotWaitForWhatAStaleSenderHolds(t *testing.T) {
	st := newSimTime()
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "v")
	start := st.ms()
	st.settle(t, w, a, b, r)
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

func TestARebasedReplicaVouchesForTheJudgeWhoseStateItHolds(t *testing.T) {
	st := newSimTime()
	w, a, j := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "j", 0)
	put(t, w.r, "/x", "v")
	st.settle(t, w, a, j)
	st.away(t, simRetention+2*simMargin, a, j)
	// x hears j once and is never settled; then x and j hear nobody else.
	x := st.replica(t, "x", 0)
	j.beat()
	st.hear(t, j, []*simReplica{x}, j)
	st.advance((gapIntervals + 1) * simRI)
	j.beat()
	x.beat()
	if !j.rejoining() || !x.rejoining() {
		t.Fatal("setup: j and x should rejoin")
	}
	j.out.take()
	st.hear(t, x, []*simReplica{j}, j, x)
	st.hear(t, j, []*simReplica{x}, j, x)
	if x.rebases() != 1 {
		t.Fatal("setup: j, which forgot what x may hold, should have rebased it")
	}
	j.beat()
	st.hear(t, j, []*simReplica{x}, j, x)
	st.hear(t, x, []*simReplica{j}, j, x)
	if j.rejoining() {
		t.Fatal("a replica rebased on a judge's state did not vouch for that judge")
	}
}

func TestARebasedJudgeDoesNotVouchBeforeItHoldsWhatItWasRebasedOn(t *testing.T) {
	st := newSimTime()
	st.advance(20 * simRI)
	w, a, b, r, j := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0), st.replica(t, "j", 0)
	put(t, w.r, "/x", "removed")
	st.settle(t, w, a, b, r, j)
	st.advance(simRI)
	del(t, a.r, "/x")
	st.exchange(t, a, b)
	st.away(t, simRetention+simMargin-3*simRI, a, b)
	// j has not heard the fleet since before the removal: a rebases it, and
	// j cannot fetch the state the verdict names.
	j.beat()
	st.hear(t, j, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{j})
	if j.rebases() != 1 {
		t.Fatalf("setup: j rebases %d", j.rebases())
	}
	q := st.replica(t, "q", 0)
	j.beat()
	st.hear(t, j, []*simReplica{q}, j)
	q.beat()
	st.hear(t, q, []*simReplica{j}, q, j)
	r.beat()
	st.hear(t, r, []*simReplica{j}, j, q)
	st.hear(t, j, []*simReplica{r}, j, q)
	if !r.rejoining() {
		t.Fatal("a judge rebased on a state it does not hold vouched for r")
	}
	for range 4 {
		st.advance(simRI)
		st.exchange(t, a, b)
		a.maintain(t)
		b.maintain(t)
	}
	compact(t, r.r)
	st.exchange(t, a, b, r, j, q)
	st.exchange(t, a, b, r, j, q)
	for _, sr := range []*simReplica{a, b, r, j, q} {
		if got := sr.value(t, "/x"); got != "<deleted>" {
			t.Errorf("replica %s: /x = %s, want <deleted>", sr.name, got)
		}
	}
}

func TestAVerdictCountsAsItsJudgesFrontier(t *testing.T) {
	st := newSimTime()
	st.advance(20 * simRI)
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, w.r, "/x", "removed")
	st.settle(t, w, a, b, r)
	before := st.ms()
	st.advance(simRI)
	del(t, a.r, "/x")
	st.exchange(t, a, b)
	// j and q hear only each other, and hold nothing: j has heard a fleet,
	// q has heard it from j.
	j, q := st.replica(t, "j", 0), st.replica(t, "q", 0)
	j.r.mu.Lock()
	j.r.since, j.r.sinceUp = before, j.r.times.up()
	j.r.mu.Unlock()
	for elapsed := time.Duration(0); elapsed < simRetention+simMargin-3*simRI; elapsed += simRI {
		st.advance(simRI)
		st.exchange(t, a, b)
		a.maintain(t)
		b.maintain(t)
		j.beat()
		q.beat()
		st.hear(t, j, []*simReplica{q})
		st.hear(t, q, []*simReplica{j})
	}
	if !j.settled() || j.rejoining() {
		t.Fatalf("setup: j should be settled with q (settled=%t rejoining=%t)", j.settled(), j.rejoining())
	}
	// j says it last heard the fleet before the removal; a tells it to
	// rebase, and j cannot fetch what a names.
	j.r.mu.Lock()
	j.r.since = before
	j.r.mu.Unlock()
	j.beat()
	st.hear(t, j, []*simReplica{a})
	st.hear(t, a, []*simReplica{j})
	if j.rebases() != 1 {
		t.Fatalf("setup: j rebases %d", j.rebases())
	}
	r.beat()
	st.hear(t, r, []*simReplica{j})
	st.hear(t, j, []*simReplica{r})
	if !r.rejoining() {
		t.Fatal("a replica rebased on a state it does not hold vouched for r before its judge's next heartbeat")
	}
	for range 4 {
		st.advance(simRI)
		st.exchange(t, a, b)
		a.maintain(t)
		b.maintain(t)
	}
	compact(t, r.r)
	st.exchange(t, a, b, r, j, q)
	st.exchange(t, a, b, r, j, q)
	for _, sr := range []*simReplica{a, b, r, j, q} {
		if got := sr.value(t, "/x"); got != "<deleted>" {
			t.Errorf("replica %s: /x = %s, want <deleted>", sr.name, got)
		}
	}
}

func TestAJudgeWaitsBeforeTrustingANewPeerThatIsNotSettled(t *testing.T) {
	healWindow(t, false, 0)
}

func TestAJudgeWaitsForAPeerItHasNotHeardForG(t *testing.T) {
	healWindow(t, true, gapIntervals+1)
}

func TestAJudgeWaitsForThePeersAPeerThatCrossedOverHeard(t *testing.T) {
	healWindow(t, true, 1)
}

func TestAPeerNamedAndNeverHeardIsWaitedForOnce(t *testing.T) {
	st := newSimTime()
	j, y := st.replica(t, "j", 0), st.replica(t, "y", 0)
	st.settle(t, j, y)
	beat := func() {
		st.advance(simRI)
		y.beat()
		for _, data := range y.out.take() {
			msg, err := j.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			msg.settled, msg.peers = false, append(msg.peers, "ghost")
			j.r.handleBroadcast(context.Background(), msg)
		}
	}
	for range gapIntervals {
		beat()
		if j.settled() {
			t.Fatal("the judge settled within G of a peer that is not settled naming one it has not heard")
		}
	}
	beat()
	beat()
	if !j.settled() {
		t.Fatal("the judge still waits, past G, for a peer it never heard")
	}
}

func TestAJudgeWaitsAgainForThePeersABridgeNamesWhenItTurns(t *testing.T) {
	bridgeTurns(t, "")
}

func TestAJudgeWaitsAgainForAPeerItHeardBeforeABridgeTurned(t *testing.T) {
	bridgeTurns(t, "before")
}

func TestAJudgeWaitsAgainForAPeerWhoseOldHeartbeatArrivesAfterTheTurn(t *testing.T) {
	bridgeTurns(t, "late")
}

func TestAJudgeWaitsAgainWhenTheBridgesOlderHeartbeatArrivesAfterItsTurn(t *testing.T) {
	bridgeTurns(t, "reordered")
}

func TestAnOlderHeartbeatOfTheNamedPeerDoesNotReplaceItsNewerFrontier(t *testing.T) {
	bridgeTurns(t, "newer")
}

func TestATurnStampedByAClockThatSteppedBackIsTaken(t *testing.T) {
	bridgeTurns(t, "stepped")
}

// bridgeTurns is the turn of TestAJudgeWaitsAgainForThePeersABridgeNamesWhenItTurns,
// the judge hearing p just before it ("before"), or hearing p's heartbeat
// sent then ("late") or y's ("reordered") only after the turn, or p's
// only after p's frontier sent past the turn ("newer"), or y's turn stamped
// by a clock that stepped back ("stepped").
func bridgeTurns(t *testing.T, variant string) {
	st := newSimTime()
	st.advance(20 * simRI)
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	j, y, p := st.replica(t, "j", 0), st.replica(t, "y", 0), st.replica(t, "p", 0)
	put(t, w.r, "/x", "removed")
	round := func(side ...*simReplica) {
		st.advance(simRI)
		st.exchange(t, side...)
		st.exchange(t, j, y)
		st.exchange(t, y, p)
	}
	for range gapIntervals + 2 {
		round(w, a, b, r)
	}
	st.advance(simRI)
	del(t, a.r, "/x")
	// a and b on one side, j and y on the other with p beyond y, r cut
	// off from both, until the removal has expired and is not forgotten yet.
	for elapsed := time.Duration(0); elapsed < simRetention+simMargin-4*simRI; elapsed += simRI {
		round(a, b)
		for _, sr := range []*simReplica{a, b, j, y, p} {
			sr.maintain(t)
		}
	}
	if !j.settled() || !y.settled() {
		t.Fatalf("setup: j and y should be settled (j=%t y=%t)", j.settled(), y.settled())
	}
	if variant == "before" {
		p.beat()
		st.hear(t, p, []*simReplica{j}, p)
		if !j.settled() {
			t.Fatal("setup: j should be settled on hearing p, settled")
		}
	}
	var old [][]byte
	switch variant {
	case "late", "newer":
		p.beat()
		old = p.out.take()
	case "reordered":
		y.beat()
		old = y.out.take()
	}
	// p hears a and b and fetches what they hold; y hears p and fetches
	// nothing yet; j hears y, then r's rejoin.
	st.listen(t, []*simReplica{a, b, p})
	p.beat()
	st.hear(t, p, []*simReplica{y})
	y.beat()
	if y.settled() {
		t.Fatal("setup: y should not be settled, short of what p holds")
	}
	turn := y.out.take()
	j.reach(j, y)
	for _, data := range turn {
		msg, err := j.r.decodeBroadcast(data)
		if err != nil {
			t.Fatal(err)
		}
		if variant == "stepped" {
			msg.now -= uint64((2 * simS).Milliseconds())
		}
		j.r.handleBroadcast(context.Background(), msg)
	}
	if variant == "newer" {
		st.advance(2 * simS)
		p.beat()
		st.hear(t, p, []*simReplica{j})
	}
	j.reach(p, y)
	for _, data := range old {
		msg, err := j.r.decodeBroadcast(data)
		if err != nil {
			t.Fatal(err)
		}
		j.r.handleBroadcast(context.Background(), msg)
	}
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	st.hear(t, r, []*simReplica{j}, j)
	st.hear(t, j, []*simReplica{r}, j)
	vouched := !r.rejoining()
	// a, b and p forget the removal before anything of r reaches them.
	for range 4 {
		st.advance(simRI)
		st.exchange(t, a, b, p)
		for _, sr := range []*simReplica{a, b, p} {
			sr.maintain(t)
		}
	}
	compact(t, r.r)
	all := []*simReplica{a, b, p, r, j, y}
	st.exchange(t, all...)
	st.exchange(t, all...)
	for _, sr := range all {
		if got := sr.value(t, "/x"); got != "<deleted>" {
			t.Errorf("replica %s: /x = %s, want <deleted> (j vouched for r: %t)", sr.name, got, vouched)
		}
	}
}

func TestABridgeHoldsAJudgeBackAtMostGEachTimeItTurns(t *testing.T) {
	st := newSimTime()
	j, y := st.replica(t, "j", 0), st.replica(t, "y", 0)
	st.settle(t, j, y)
	// A turn is the bridge's frontier marked with its own count, as one
	// that comes to lack what a peer holds marks it.
	var mark uint64
	beat := func(settled, turns bool, incarnation string) {
		st.advance(simRI)
		j.beat()
		st.hear(t, j, []*simReplica{y}, j)
		y.beat()
		for _, data := range y.out.take() {
			msg, err := j.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			if turns {
				mark = msg.sent
			}
			msg.settled, msg.turned, msg.peers = settled, mark, append(msg.peers, "ghost")
			if incarnation != "" {
				msg.incarnation = incarnation
			}
			j.r.handleBroadcast(context.Background(), msg)
		}
	}
	for turn := range 3 {
		beat(true, false, "")
		if !j.settled() {
			t.Fatalf("turn %d: a bridge that says it is settled held the judge back", turn)
		}
		for i := range gapIntervals {
			beat(false, i == 0, "")
			if j.settled() {
				t.Fatalf("turn %d: the judge settled %d intervals after a bridge naming a peer it never heard turned", turn, i+1)
			}
		}
		beat(false, false, "")
		beat(false, false, "")
		if !j.settled() {
			t.Fatalf("turn %d: the judge still waits, past G, for a peer a bridge named on turning", turn)
		}
	}
	inc := y.incarnation()
	beat(false, true, inc[:len(inc)-nonceLen]+strings.Repeat("z", nonceLen))
	if !j.settled() {
		t.Fatal("a bridge's new incarnation that is not settled renewed the wait")
	}
}

func TestATurnHoldsAJudgeBackUntilTheBridgeSettles(t *testing.T) {
	st := newSimTime()
	j, y, q := st.replica(t, "j", 0), st.replica(t, "y", 0), st.replica(t, "q", 0)
	st.settle(t, j, y, q)
	turn := func(settled bool) {
		y.beat()
		for _, data := range y.out.take() {
			msg, err := j.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			msg.settled = settled
			if !settled {
				msg.turned = msg.sent
			}
			j.r.handleBroadcast(context.Background(), msg)
		}
	}
	st.advance(simRI)
	turn(false)
	for i := range gapIntervals - 1 {
		if j.settled() {
			t.Fatalf("the judge settled %d intervals after a bridge's turn naming a peer it hears", i)
		}
		st.advance(simRI)
		q.beat()
		st.hear(t, q, []*simReplica{j}, q)
	}
	if j.settled() {
		t.Fatal("hearing the named peer after the turn ended the wait")
	}
	turn(true)
	if !j.settled() {
		t.Fatal("the judge still waits once the bridge says it is settled")
	}
}

func TestATurnIsTheSendersToTell(t *testing.T) {
	senderTurns(t, false)
}

func TestATurnBetweenTwoFramesIsATurn(t *testing.T) {
	senderTurns(t, true)
}

// senderTurns is TestATurnIsTheSendersToTell, y catching up and falling
// short again between two frontiers if between.
func senderTurns(t *testing.T, between bool) {
	st := newSimTime()
	j, y, q := st.replica(t, "j", 0), st.replica(t, "y", 0), st.replica(t, "q", 0)
	st.settle(t, j, y, q)
	send := func(sr *simReplica) [][]byte {
		sr.beat()
		return sr.out.take()
	}
	deliver := func(msgs [][]byte) {
		for _, data := range msgs {
			msg, err := j.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			j.r.handleBroadcast(context.Background(), msg)
		}
	}
	lag := func(v string) {
		st.advance(simRI)
		put(t, q.r, "/q", v)
		q.beat()
		st.hear(t, q, []*simReplica{y})
		if y.settled() {
			t.Fatal("setup: y should not be settled, short of what q wrote")
		}
	}
	lag("1")
	j.pull(t, q)
	deliver(send(y))
	// y stays short of q's write for longer than G: the waits its first
	// turn opened are over.
	for range 3 * gapIntervals {
		st.advance(simRI)
		q.beat()
		st.hear(t, q, []*simReplica{j, y})
		j.beat()
		st.hear(t, j, []*simReplica{y, q})
		deliver(send(y))
	}
	st.advance(simRI)
	y.pull(t, q)
	if !y.settled() {
		t.Fatal("setup: y should be settled once it holds what q wrote")
	}
	var settled [][]byte
	if !between {
		settled = send(y)
	}
	lag("2")
	deliver(send(y))
	deliver(settled)
	if j.settled() {
		t.Fatal("the judge settled on a bridge's turn it saw only through what the bridge said")
	}
}

func TestAFrontierDoesNotGoBack(t *testing.T) {
	st := newSimTime()
	j, y := st.replica(t, "j", 0), st.replica(t, "y", 0)
	st.settle(t, j, y)
	beat := func() message {
		st.advance(simRI)
		y.beat()
		var hb message
		for _, data := range y.out.take() {
			msg, err := j.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			if msg.context != nil {
				hb = msg
			}
		}
		if hb.context == nil {
			t.Fatal("setup: y sent no frontier")
		}
		return hb
	}
	held := func() *frontier {
		j.r.mu.Lock()
		defer j.r.mu.Unlock()
		return j.r.frontiers[y.incarnation()]
	}
	older, newer := beat(), beat()
	older.settled, newer.settled = true, false
	j.r.handleBroadcast(context.Background(), newer)
	j.r.handleBroadcast(context.Background(), older)
	if f := held(); f.settled || f.sent != newer.sent {
		t.Fatalf("an older frontier replaced a newer one (settled=%t sent=%d, want %d)", f.settled, f.sent, newer.sent)
	}
	stepped := beat()
	stepped.settled, stepped.now = true, older.now
	j.r.handleBroadcast(context.Background(), stepped)
	if f := held(); !f.settled || f.sent != stepped.sent {
		t.Fatalf("a frontier sent later under a clock that stepped back was not taken (settled=%t sent=%d, want %d)", f.settled, f.sent, stepped.sent)
	}
}

func TestAVerdictNamesThePeersItsJudgeHeard(t *testing.T) {
	st := newSimTime()
	j, k, q, z := st.replica(t, "j", 0), st.replica(t, "k", 0), st.replica(t, "q", 0), st.replica(t, "z", 0)
	put(t, z.r, "/x", "v")
	for range gapIntervals + 2 {
		st.advance(simRI)
		st.exchange(t, j, k)
		st.exchange(t, q, z)
	}
	if !j.settled() || !k.settled() || !q.settled() {
		t.Fatal("setup: j and k, q and z, should each be settled")
	}
	// k hears q, and cannot fetch what it holds; j hears k only.
	q.beat()
	st.hear(t, q, []*simReplica{k})
	k.beat()
	st.hear(t, k, []*simReplica{j})
	if k.settled() || j.settled() {
		t.Fatalf("setup: k should not be settled, nor j while k names q (k settled=%t, j settled=%t)", k.settled(), j.settled())
	}
	k.r.mu.Lock()
	hs, _ := k.r.heads.List()
	b := k.r.message(hs, &verdict{to: "elsewhere", rebase: true, since: k.r.reportedSince()})
	k.r.mu.Unlock()
	data, err := proto.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := j.r.decodeBroadcast(data)
	if err != nil {
		t.Fatal(err)
	}
	j.r.handleBroadcast(context.Background(), msg)
	if j.settled() {
		t.Fatal("a verdict of a judge that is not settled ended the wait for the peer its heartbeat named")
	}
}

// healWindow is the heal of TestAJudgeWaitsBeforeTrustingANewPeerThatIsNotSettled,
// the peer first heard of the other side one the judge heard before the
// partition if known, gone over for the last intervals over of it.
func healWindow(t *testing.T, known bool, over int) {
	st := newSimTime()
	st.advance(20 * simRI)
	w, a, b, r := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	j, x := st.replica(t, "j", 0), st.replica(t, "x", 0)
	jside := []*simReplica{j, x}
	var y *simReplica
	if known {
		y = st.replica(t, "y", 0)
		jside = append(jside, y)
	}
	put(t, w.r, "/x", "removed")
	for range gapIntervals + 2 {
		st.advance(simRI)
		st.exchange(t, w, a, b, r)
		st.exchange(t, jside...)
	}
	st.advance(simRI)
	del(t, a.r, "/x")
	// a and b on one side, j and x on the other, r cut off from both, until
	// the removal has expired and is not forgotten yet; y, if known, goes
	// over for the last of it and hears a and b, holding nothing of theirs.
	end := simRetention + simMargin - 4*simRI
	for elapsed := time.Duration(0); elapsed < end; elapsed += simRI {
		if known && end-elapsed <= time.Duration(over)*simRI {
			st.listen(t, []*simReplica{a, b}, y)
			st.exchange(t, j, x)
			j.maintain(t)
			x.maintain(t)
			continue
		}
		st.advance(simRI)
		for _, side := range [][]*simReplica{{a, b}, jside} {
			st.exchange(t, side...)
			for _, sr := range side {
				sr.maintain(t)
			}
		}
	}
	// y joins a and b's side and hears them, holding nothing.
	if !known {
		y = st.replica(t, "y", 0)
	}
	st.listen(t, []*simReplica{a, b}, y)
	if known && y.rejoining() {
		t.Fatal("setup: y should hear a and b as a live peer does")
	}
	if !j.settled() {
		t.Fatal("setup: j should be settled on its side")
	}
	// The heal: j hears y first, then r's rejoin.
	y.beat()
	st.hear(t, y, []*simReplica{j}, y)
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	st.hear(t, r, []*simReplica{j}, j, x)
	st.hear(t, j, []*simReplica{r}, j, x)
	if !r.rejoining() {
		t.Fatal("a judge that first heard a new peer that is not settled vouched for r at once")
	}
	for range 4 {
		st.advance(simRI)
		st.exchange(t, a, b)
		a.maintain(t)
		b.maintain(t)
	}
	compact(t, r.r)
	st.exchange(t, a, b, r, j, x, y)
	st.exchange(t, a, b, r, j, x, y)
	for _, sr := range []*simReplica{a, b, r, j, x, y} {
		if got := sr.value(t, "/x"); got != "<deleted>" {
			t.Errorf("replica %s: /x = %s, want <deleted>", sr.name, got)
		}
	}
}

// restartEmpty is a crash and restart with nothing on disk, the replica's id
// kept.
func (sr *simReplica) restartEmpty(t testing.TB) {
	t.Helper()
	closeStore(t, sr.r)
	forgetIncarnation(sr.r.incarnationKey())
	sr.store, sr.dag = newSortedStore(), newFetchThrough()
	sr.open(t)
}

func TestARejoinEndsWhileAPeerKeepsRestarting(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b, r)
	c := st.replica(t, "c", 0)
	round := func() {
		st.listen(t, []*simReplica{a, b, r}, c)
		c.beat()
		st.hear(t, c, []*simReplica{a, b, r})
	}
	round()
	if a.settled() {
		t.Fatal("setup: a should wait for c, a new peer that is not settled")
	}
	r.reopen(t)
	for i := 1; r.rejoining(); i++ {
		if i > gapIntervals+2 {
			t.Fatalf("a peer restarting faster than G kept every judge from vouching for %d intervals", i-1)
		}
		if i%2 == 0 {
			inc := c.incarnation()
			c.restartEmpty(t)
			if c.incarnation() == inc {
				t.Fatal("setup: c should restart under a new incarnation")
			}
		}
		round()
	}
}

func TestAJudgeDoesNotWaitForAPeerItHeardAllAlong(t *testing.T) {
	st := newSimTime()
	a, b, p, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "p", 0), st.replica(t, "r", 0)
	st.settle(t, a, b, p, r)
	p.reopen(t)
	put(t, a.r, "/w", "1")
	for range gapIntervals + 1 {
		st.listen(t, []*simReplica{a, b, r}, p)
		p.beat()
		st.hear(t, p, []*simReplica{a, b, r})
	}
	if !p.rejoining() {
		t.Fatal("setup: p should still rejoin")
	}
	// p fetches what the oks vouched with from b, and hears what a wrote
	// since without fetching it: its rejoin ends, and it is not settled.
	p.pull(t, b)
	put(t, a.r, "/u", "1")
	a.beat()
	st.hear(t, a, []*simReplica{p})
	p.beat()
	if p.rejoining() || p.settled() {
		t.Fatalf("setup: p should have rejoined, not settled (rejoining=%t settled=%t)", p.rejoining(), p.settled())
	}
	st.hear(t, p, []*simReplica{a})
	r.reopen(t)
	r.beat()
	st.hear(t, r, []*simReplica{a}, a)
	st.hear(t, a, []*simReplica{r}, a)
	if r.rejoining() {
		t.Fatal("a judge waited, as for a newcomer, for a peer it heard all along")
	}
}

func TestAJudgeThatKeepsWritingDoesNotStarveARejoin(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	st.settle(t, a, b, r)
	r.reopen(t)
	var late []Head
	for i := range 3 {
		if !r.rejoining() {
			return
		}
		st.advance(simRI)
		put(t, a.r, "/progress", fmt.Sprint(i))
		st.exchange(t, a, b)
		r.beat()
		st.hear(t, r, []*simReplica{a}, a, b)
		var heads []Head
		for _, data := range a.out.take() {
			msg, err := r.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			if msg.verdict != nil {
				heads = msg.heads
			}
			r.reach()
			r.r.handleBroadcast(context.Background(), msg)
		}
		if len(heads) == 0 {
			t.Fatalf("setup: no verdict in round %d", i)
		}
		r.reach(a, b)
		for _, h := range late {
			if err := r.r.handleBlockAt(context.Background(), r.r.currentGeneration(), h); err != nil {
				t.Fatal(err)
			}
		}
		late = heads
	}
	if r.rejoining() {
		t.Fatal("a rejoin answered every interval by a judge that writes, its blocks an interval late, did not end")
	}
}

func TestARejoiningReplicaForgetsPeersItNoLongerHears(t *testing.T) {
	st := newSimTime()
	a, r := st.replica(t, "a", 0), st.replica(t, "r", 0)
	st.settle(t, a, r)
	r.reopen(t)
	for i := range 30 {
		st.advance(simRI)
		r.beat()
		r.r.handleBroadcast(context.Background(), message{incarnation: fmt.Sprint("peer-", i), since: st.ms(), now: st.ms(), context: newCausalContext()})
	}
	if !r.rejoining() {
		t.Fatal("setup: r should still rejoin")
	}
	r.r.mu.Lock()
	n, m := len(r.r.frontiers), len(r.r.contacts)
	r.r.mu.Unlock()
	if n > gapIntervals+1 || m > gapIntervals+1 {
		t.Fatalf("a rejoining replica keeps %d frontiers and %d contacts of peers heard once, want at most %d each", n, m, gapIntervals+1)
	}
}

func TestAnOkWhoseJudgeForgotPartOfItStillEndsTheRejoin(t *testing.T) {
	st := newSimTime()
	w := st.replica(t, "w", 0)
	a := st.replica(t, "a", 0)
	b := st.replica(t, "b", 0)
	put(t, w.r, "/old", "v")
	writer := w.incarnation()
	st.settle(t, w, a, b)

	// w's entry expires at T+m; r's fleet starts shortly before, so what w
	// last changed predates r's gap.
	st.away(t, simRetention-10*simRI, a, b)
	r := st.replica(t, "r", 0)
	q := st.replica(t, "q", 0)
	for range gapIntervals + 2 {
		st.advance(simRI)
		st.exchange(t, a, b)
		st.exchange(t, r, q)
		a.maintain(t)
		b.maintain(t)
	}
	if !a.settled() || !r.settled() {
		t.Fatal("setup: both fleets should be settled")
	}

	r.reopen(t)
	r.beat()
	st.hear(t, r, []*simReplica{a}, a, b)
	st.hear(t, a, []*simReplica{r})
	r.r.mu.Lock()
	first := r.r.oks[a.incarnation()]
	pinned := first != nil && first.cc.inc[writer] != nil
	r.r.mu.Unlock()
	if !pinned {
		t.Fatal("setup: the first ok must carry w's entry")
	}

	for elapsed := time.Duration(0); elapsed < 2*simMargin; elapsed += simRI {
		st.listen(t, []*simReplica{a, b}, r)
		r.beat()
		st.hear(t, r, []*simReplica{a}, a, b)
		if elapsed >= simMargin {
			// r fetches again once a and b have forgotten w.
			st.hear(t, a, []*simReplica{r}, a, b)
		} else {
			st.hear(t, a, []*simReplica{r})
		}
	}
	if a.knows(writer) || r.knows(writer) {
		t.Fatal("setup: w's entry should be forgotten")
	}
	a.r.mu.Lock()
	current := a.r.cc.clone()
	a.r.mu.Unlock()
	r.r.mu.Lock()
	holdsCurrent := r.r.holds(current)
	r.r.mu.Unlock()
	if !holdsCurrent {
		t.Fatal("setup: r must hold what its judge holds now")
	}
	if r.rejoining() {
		t.Fatal("the rejoin waits for an entry its judge has forgotten")
	}
}
