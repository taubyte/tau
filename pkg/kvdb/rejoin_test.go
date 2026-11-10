package kvdb

import (
	"context"
	"testing"
	"time"

	ds "github.com/ipfs/go-datastore"
	"google.golang.org/protobuf/proto"
)

func (sr *simReplica) rejoining() bool {
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	return sr.r.rejoining
}

func (sr *simReplica) incarnation() string { return sr.r.currentIncarnation() }

// away keeps inContact in contact for d of virtual time, beating and
// maintaining every rebroadcast interval, while the others hear nothing.
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
	put(t, b.r, "/y", "1")
	b.beat()
	st.deliver(t, b, a)
	a.wantValue(t, "/y", "<deleted>") // nobody walks a rejoining replica
	st.deliver(t, a, b)
	if b.rejoining() {
		t.Fatal("an ok from a peer did not end the rejoin")
	}
	st.exchange(t, a, b)
	a.wantValue(t, "/y", "1")
}

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
	c.beat()
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
	st.deliver(t, a, c)
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

func TestALateVerdictIsDeadOnArrival(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	del(t, a.r, "/k")
	st.away(t, simRetention+2*simMargin, a, b)

	c.beat()
	st.deliver(t, c, a, b)
	first := a.out.take()
	second := b.out.take()
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("setup: both judges should have answered")
	}
	a.out.msgs = first
	before := c.incarnation()
	st.deliver(t, a, c)
	rebased := c.incarnation()
	if rebased == before {
		t.Fatal("setup: the first verdict should have rebased c")
	}
	put(t, c.r, "/after", "1")

	b.out.msgs = second
	st.deliver(t, b, c)
	if c.incarnation() != rebased {
		t.Fatal("a verdict for an incarnation that is gone was acted on")
	}
	c.wantValue(t, "/after", "1")
}

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
	if msgs := slow.out.take(); len(msgs) != 0 {
		t.Fatal("a judge answered a replica whose clock it cannot trust")
	}
	// An ok it sent all the same is not taken.
	slow.r.mu.Lock()
	ok := slow.r.message(nil, &verdict{to: c.incarnation(), since: slow.r.since, held: slow.r.since})
	slow.r.mu.Unlock()
	data, err := proto.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	slow.out.msgs = [][]byte{data}
	st.deliver(t, slow, c)
	if !c.rejoining() {
		t.Fatal("a stale replica took an ok from a judge whose clock is behind")
	}
	c.wantValue(t, "/k", "v")

	st.exchange(t, a, b, c)
	st.exchange(t, a, b, c)
	c.wantValue(t, "/k", "<deleted>")
	a.wantValue(t, "/k", "<deleted>")
}

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

func TestAPeersAcksForgetTheOwnOperations(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "1")
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 0 {
		t.Fatalf("a replica that never heard a fleet logs %d own operations", n)
	}
	st.exchange(t, a, b)
	put(t, a.r, "/k", "2")
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 2 {
		t.Fatalf("a keeps %d own operations, want the two nobody acknowledged yet", n)
	}
	st.exchange(t, a, b)
	st.exchange(t, a, b)
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 0 {
		t.Fatalf("a keeps %d own operations its peer acknowledged", n)
	}
}

func TestAMergeThatGrowsALaterSpanIsAcknowledged(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	st.exchange(t, a, b)
	put(t, a.r, "/k1", "v")
	st.exchange(t, a, b)
	st.exchange(t, a, b)
	put(t, a.r, "/k2", "v")
	third := put(t, a.r, "/k3", "v")
	fourth := put(t, a.r, "/k4", "v")
	b.mergeBlock(a, third)
	st.advance((gapIntervals + 1) * simRI)
	b.mergeBlock(a, fourth)
	b.beat()
	st.deliver(t, b, a)
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 1 {
		t.Fatalf("a keeps %d own operations, want only the one its peer missed", n)
	}
}

func TestARebaseInterruptedByACrashFinishesAtOpen(t *testing.T) {
	for _, before := range []string{"the writes", "the snapshot"} {
		t.Run(before, func(t *testing.T) {
			st := newSimTime()
			a, c := st.replica(t, "a", 0), st.replica(t, "c", 0)
			faulty := newFaultyDatastore(c.store)
			c.store = faulty
			c.reopen(t)
			put(t, a.r, "/k", "v")
			st.exchange(t, a, c)
			put(t, c.r, "/p", "mine")
			ctx := context.Background()

			if before == "the writes" {
				// The drop commits, and nothing after it.
				commits := 0
				faulty.SetFail(func(op string, _ ds.Key) error {
					if op != "Commit" {
						return nil
					}
					if commits++; commits > 1 {
						return errFault
					}
					return nil
				})
			}
			c.r.mu.Lock()
			err := c.r.dropAllButOwn(ctx, st.ms())
			c.r.mu.Unlock()
			if (err != nil) != (before == "the writes") {
				t.Fatalf("setup: the drop returned %v", err)
			}
			faulty.SetFail(nil)
			if before == "the writes" {
				// A compaction before the crash does not carry what was
				// not written.
				compact(t, c.r)
			}
			// The crash: the store as the rebase left it, reopened.
			c.reopen(t)
			c.wantValue(t, "/p", "mine")
			c.maintain(t)
			if has, _ := c.store.Has(ctx, c.r.rebaseKey()); has {
				t.Fatal("the rebase is still marked after the open wrote its operations")
			}
			st.exchange(t, a, c)
			st.exchange(t, a, c)
			a.wantValue(t, "/p", "mine")
			c.wantValue(t, "/k", "v")
		})
	}
}

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

func TestAJudgeThatHeardLessIsNotTrusted(t *testing.T) {
	st := newSimTime()
	a, c := st.replica(t, "a", 0), st.replica(t, "c", 0)
	put(t, a.r, "/k", "v")
	st.exchange(t, a, c)
	st.away(t, 3*simRI, a, c)
	c.reopen(t)
	gap := c.r.reportedSinceNow()

	behind := st.replica(t, "behind", 0)
	behind.r.mu.Lock()
	behind.r.since = gap - 1 // heard the fleet, a moment before c left it
	behind.r.mu.Unlock()
	c.beat()
	st.deliver(t, c, behind)
	if msgs := behind.out.take(); len(msgs) != 0 {
		t.Fatal("a judge that heard an older fleet than the replica answered it")
	}
	behind.r.mu.Lock()
	ok := behind.r.message(nil, &verdict{to: c.incarnation(), since: gap - 1, held: gap - 1})
	behind.r.mu.Unlock()
	data, err := proto.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	behind.out.msgs = [][]byte{data}
	st.deliver(t, behind, c)
	if !c.rejoining() {
		t.Fatal("a rejoining replica took an ok from a judge that heard an older fleet than it had")
	}
	// Nor from one that has heard a recent fleet and holds what the fleet
	// held only as of before c left it.
	behind.r.mu.Lock()
	ok = behind.r.message(nil, &verdict{to: c.incarnation(), since: gap, held: gap - 1})
	behind.r.mu.Unlock()
	data, err = proto.Marshal(ok)
	if err != nil {
		t.Fatal(err)
	}
	behind.out.msgs = [][]byte{data}
	st.deliver(t, behind, c)
	if !c.rejoining() {
		t.Fatal("a rejoining replica took an ok from a judge that held the fleet's state only as of before it left")
	}
	st.exchange(t, a, c)
	if c.rejoining() {
		t.Fatal("a judge that heard as much did not end the rejoin")
	}
}

func TestASlowAuthorStampsTheFleetTimeItHeard(t *testing.T) {
	st := newSimTime()
	a, b, slow := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "slow", -5*simS/2)
	put(t, a.r, "/k", "v")
	// Its own value: what the others write is ahead of its clock, and waits.
	put(t, slow.r, "/s", "v")
	st.exchange(t, a, b, slow)
	// A rejoin ends once the replica holds its judge's state, and the
	// judge's blocks carry the fleet's time, ahead of this clock: they must
	// first age into its reach.
	for range gapIntervals + 3 {
		st.advance(simRI)
		st.exchange(t, a, b, slow)
	}
	slow.reopen(t)
	st.exchange(t, a, b, slow)
	if slow.rejoining() {
		t.Fatal("setup: slow should have rejoined")
	}
	d := deltaAt(t, slow, del(t, slow.r, "/s"))
	if new(internTable).dotFromPB(d.GetDot()).replica != slow.incarnation() || len(d.GetTombstones()) == 0 {
		t.Fatal("setup: slow's removal wrote no block")
	}
	if now := st.ms(); d.GetAt()+durationMillis(simS) < now {
		t.Fatalf("a slow author stamped its removal %dms behind the fleet's time", now-d.GetAt())
	}
}

func TestAReplicaThatHearsAPeerAfterAGapRejoinsFirst(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/x", "removed")
	st.exchange(t, w, a, b, c)
	del(t, b.r, "/x")
	st.away(t, simRetention+2*simMargin, b, c)
	b.wantValue(t, "/x", "<deleted>")

	b.beat()
	st.deliver(t, b, a)
	if !a.rejoining() {
		t.Fatal("a replica back from a gap took the first peer it heard for contact")
	}
	a.beat()
	a.maintain(t)
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/x", "<deleted>")
	}
}

func TestAnOwnWriteSurvivesASecondRebase(t *testing.T) {
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	st.exchange(t, a, b, c)
	put(t, a.r, "/own", "unsent")
	a.out.take()
	for i, name := range []string{"w1", "w2"} {
		st.advance(simRI)
		w := st.replica(t, name, 0)
		put(t, w.r, "/other", "v")
		st.exchange(t, w, b, c)
		del(t, b.r, "/other")
		st.away(t, simRetention+2*simMargin, b, c)
		a.beat()
		st.deliver(t, a, b)
		st.deliver(t, b, a)
		a.out.take()
		if got := a.rebases(); got != i+1 {
			t.Fatalf("setup: %d rebases, want %d", got, i+1)
		}
		a.wantValue(t, "/own", "unsent")
	}
}

// dropForRebase is a rebase up to releasing the lock: what follows happens on
// the state it leaves, before its walk ends.
func (sr *simReplica) dropForRebase(t testing.TB, since uint64) {
	t.Helper()
	sr.r.mu.Lock()
	defer sr.r.mu.Unlock()
	if err := sr.r.dropAllButOwn(context.Background(), since); err != nil {
		t.Fatal(err)
	}
}

// walked ends a rebase's walk without the judge's state fetched.
func (sr *simReplica) walked(t testing.TB) {
	t.Helper()
	if err := sr.r.rebuild(context.Background(), sr.r.currentGeneration(), 0, message{}); err != nil {
		t.Fatal(err)
	}
}

func TestAnOwnWriteSurvivesARebaseBeforeTheLastOneIsWalked(t *testing.T) {
	st := newSimTime()
	a, r := st.replica(t, "a", 0), st.replica(t, "r", 0)
	st.settle(t, a, r)
	put(t, r.r, "/only", "mine")
	r.dropForRebase(t, st.ms())
	r.dropForRebase(t, st.ms())
	r.walked(t)
	r.wantValue(t, "/only", "mine")
}

func TestAMaintenanceWhileARebaseWalksKeepsTheOwnWrites(t *testing.T) {
	st := newSimTime()
	a, b, r := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "r", 0)
	st.settle(t, a, b, r)
	put(t, r.r, "/only", "mine")
	st.away(t, simRetention+2*simMargin, a, b)
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	r.dropForRebase(t, st.ms())
	r.maintain(t)
	r.walked(t)
	r.wantValue(t, "/only", "mine")
}

func TestADeleteWhileARebaseWalksStaysDeleted(t *testing.T) {
	st := newSimTime()
	a, r := st.replica(t, "a", 0), st.replica(t, "r", 0)
	st.settle(t, a, r)
	put(t, r.r, "/only", "mine")
	r.dropForRebase(t, st.ms())
	del(t, r.r, "/only")
	r.walked(t)
	r.wantValue(t, "/only", "<deleted>")
}

func TestAWriteBeforeFirstContactSurvivesARebase(t *testing.T) {
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, a.r, "/own", "unsent")
	a.out.take()
	st.exchange(t, b, c)
	b.beat()
	st.deliver(t, b, a)
	a.out.take()
	w := st.replica(t, "w", 0)
	put(t, w.r, "/other", "v")
	st.exchange(t, w, b, c)
	del(t, b.r, "/other")
	st.away(t, simRetention+2*simMargin, b, c)
	a.beat()
	st.exchange(t, a, b, c)
	if a.rebases() != 1 {
		t.Fatalf("setup: %d rebases, want 1", a.rebases())
	}
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/own", "unsent")
	}
}

func TestARemovalBeforeFirstContactSurvivesARebase(t *testing.T) {
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, a.r, "/x", "removed")
	st.deliver(t, a, b, c)
	b.wantValue(t, "/x", "removed")
	del(t, a.r, "/x")
	a.out.take()
	st.exchange(t, b, c)
	b.beat()
	st.deliver(t, b, a)
	a.out.take()
	w := st.replica(t, "w", 0)
	put(t, w.r, "/other", "v")
	st.exchange(t, w, b, c)
	del(t, b.r, "/other")
	st.away(t, simRetention+2*simMargin, b, c)
	a.beat()
	st.exchange(t, a, b, c)
	if a.rebases() != 1 {
		t.Fatalf("setup: %d rebases, want 1", a.rebases())
	}
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/x", "<deleted>")
	}
}

func TestAReplicaThatWroteBeforeAnyContactIsJudgedAtFirstContact(t *testing.T) {
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, a.r, "/x", "removed")
	st.deliver(t, a, b, c)
	b.wantValue(t, "/x", "removed")
	del(t, b.r, "/x")
	st.away(t, simRetention+2*simMargin, b, c)
	a.maintain(t)
	if n := countEntries(t, a.r, a.r.ownPrefix()); n != 0 {
		t.Fatalf("setup: a still logs %d writes", n)
	}

	b.beat()
	st.deliver(t, b, a)
	if !a.rejoining() {
		t.Fatal("a replica that wrote before it heard anyone took the first fleet time it heard")
	}
	a.maintain(t)
	st.exchange(t, a, b, c)
	if a.rebases() != 1 {
		t.Fatalf("%d rebases, want 1", a.rebases())
	}
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/x", "<deleted>")
	}
}

func TestACopyRestoredFromBeforeFirstContactIsJudged(t *testing.T) {
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, a.r, "/x", "removed")
	saved, blocks := copyStore(t, a.store), copyBlocks(t, a.dag)
	st.exchange(t, a, b, c)
	del(t, b.r, "/x")
	st.exchange(t, a, b, c)
	st.away(t, simRetention+2*simMargin, b, c)
	closeStore(t, a.r)
	forgetIncarnation(a.r)
	a.store, a.dag = saved, blocks
	a.open(t)
	a.maintain(t)
	st.exchange(t, a, b, c)
	if !a.rejoining() {
		t.Fatal("a copy from before first contact took the first fleet time it heard")
	}
	st.exchange(t, a, b, c)
	if a.rebases() != 1 {
		t.Fatalf("%d rebases, want 1", a.rebases())
	}
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/x", "<deleted>")
	}
}

func TestAnExpiredIncarnationLeavesTheAcknowledgements(t *testing.T) {
	st := newSimTime()
	w, a, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "c", 0)
	put(t, w.r, "/x", "v")
	inc := w.incarnation()
	st.exchange(t, w, a, c)
	st.away(t, simRetention+simMargin-simRI, a, c)
	b := st.replica(t, "b", 0)
	st.exchange(t, a, b, c)
	if !b.knows(inc) {
		t.Fatal("setup: b should know the writer")
	}
	st.advance(2 * simRI)
	b.maintain(t)
	st.away(t, 3*simRetention, a, b, c)
	b.r.mu.Lock()
	_, kept := b.r.merged[inc]
	b.r.mu.Unlock()
	if kept {
		t.Fatal("an incarnation forgotten stayed among the acknowledgements")
	}
}

func TestAJudgeStillWalkingTheFleetKnowsWhatItForgot(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/x", "removed")
	st.exchange(t, w, a, b, c)
	del(t, b.r, "/x")
	st.away(t, simRetention+2*simMargin, b, c)

	f := st.replica(t, "f", 0)
	b.beat()
	f.reach()
	for _, data := range b.out.take() {
		msg, err := f.r.decodeBroadcast(data)
		if err != nil {
			t.Fatal(err)
		}
		f.r.handleBroadcast(context.Background(), msg)
	}
	f.out.take()
	if f.horizon() < b.horizon() {
		t.Fatalf("f heard b's heartbeat and knows a horizon of %d, b's is %d", f.horizon(), b.horizon())
	}
	a.beat()
	if !a.rejoining() {
		t.Fatal("setup: a should rejoin")
	}
	st.deliver(t, a, f)
	st.deliver(t, f, a)
	if a.rebases() != 1 {
		t.Fatal("a judge holding none of the fleet's blocks vouched for a replica away longer than T")
	}
	st.exchange(t, a, b, c, f)
	for _, sr := range []*simReplica{a, b, c, f} {
		sr.wantValue(t, "/x", "<deleted>")
	}
}

func TestTwoReplicasLeftToEachOtherStayTogetherAfterARejoin(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, a.r, "/k", "v")
	st.settle(t, a, b)
	a.reopen(t)
	deliver := func(msgs [][]byte, src, dst *simReplica) {
		dst.reach(src, dst)
		for _, data := range msgs {
			msg, err := dst.r.decodeBroadcast(data)
			if err != nil {
				t.Fatal(err)
			}
			dst.r.handleBroadcast(context.Background(), msg)
		}
	}
	var fromA, fromB [][]byte
	together := 0
	for range 12 * gapIntervals {
		st.advance(simRI)
		a.beat()
		b.beat()
		deliver(fromA, a, b)
		deliver(fromB, b, a)
		fromA, fromB = a.out.take(), b.out.take()
		if a.rejoining() || b.rejoining() {
			together = 0
		} else {
			together++
		}
	}
	if together < 4*gapIntervals {
		t.Fatalf("two replicas left to each other did not stay together after a rejoin (together for the last %d intervals; a rejoining=%t, b rejoining=%t)", together, a.rejoining(), b.rejoining())
	}
}

func TestAJudgeWhoseOkCannotEndARejoinStillRejoins(t *testing.T) {
	st := newSimTime()
	x, y := st.replica(t, "x", 0), st.replica(t, "y", 0)
	put(t, x.r, "/k", "v")
	older := st.ms()
	st.settle(t, x, y)
	x.reopen(t)
	y.r.mu.Lock()
	y.r.settledSince = older
	clear(y.r.frontiers)
	y.r.liveHeard = y.r.times.up() - 2*gapIntervals*simRI
	y.r.lastHeard = y.r.times.up()
	y.r.mu.Unlock()
	together := 0
	for range 12 * gapIntervals {
		st.advance(simRI)
		st.exchange(t, x, y)
		if x.rejoining() || y.rejoining() {
			together = 0
		} else {
			together++
		}
	}
	if together < 4*gapIntervals {
		t.Fatalf("a judge whose ok could not end a rejoin kept the two apart (together for the last %d intervals; x rejoining=%t, y rejoining=%t)", together, x.rejoining(), y.rejoining())
	}
}

func TestARejoinAmongAWrongClockAndANewReplicaEnds(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	w := st.replica(t, "w", 10*time.Minute)
	st.settle(t, a, b)
	st.exchange(t, a, b, w)
	if w.since() == 0 {
		t.Fatal("setup: w should have heard a fleet")
	}
	// b is gone: a hears only the wrong clock, and rejoins.
	for range gapIntervals + 2 {
		st.advance(simRI)
		st.exchange(t, a, w)
	}
	if !a.rejoining() || !w.rejoining() {
		t.Fatalf("setup: a and w should rejoin (a %t, w %t)", a.rejoining(), w.rejoining())
	}
	f := st.replica(t, "f", 0)
	for range 2*gapIntervals + 2 {
		st.advance(simRI)
		st.exchange(t, a, w, f)
	}
	if a.rejoining() {
		t.Fatal("a stays rejoining among a wrong clock and a replica that never heard a fleet")
	}
	if f.since() == 0 {
		t.Fatal("the new replica never heard the fleet's time")
	}
	if !w.rejoining() {
		t.Fatal("the wrong clock ended its rejoin among peers it cannot agree with")
	}
}

func TestAnOwnWriteSurvivesMaintenanceBetweenTwoRebases(t *testing.T) {
	st := newSimTime()
	a := st.replica(t, "a", 0)
	b := st.replica(t, "b", 0)
	r := st.replica(t, "r", 0)
	st.settle(t, a, b, r)
	put(t, r.r, "/only", "mine")
	r.out.take()

	st.away(t, simRetention+2*simMargin, a, b)
	r.beat()
	if !r.rejoining() {
		t.Fatal("setup: r should rejoin")
	}
	r.dropForRebase(t, st.ms())
	// The walk of the judge's state has not ended.
	r.r.mu.Lock()
	r.r.rebuilding = r.r.generation
	r.r.mu.Unlock()
	r.maintain(t)
	r.wantValue(t, "/only", "mine")

	r.dropForRebase(t, st.ms())
	r.wantValue(t, "/only", "mine")
}

func TestACompactionThatWritesNothingKeepsTheRebaseMarked(t *testing.T) {
	st := newSimTime()
	a := st.replica(t, "a", 0)
	r := st.replica(t, "r", 0)
	st.settle(t, a, r)
	put(t, r.r, "/only", "mine")
	r.out.take()
	r.dropForRebase(t, st.ms())

	// Reopened with the writes done and no snapshot carrying them.
	r.reopen(t)
	mark, err := r.store.Get(context.Background(), r.r.rebaseKey())
	if err != nil || len(mark) == 0 || r.r.heads.Len() != 0 {
		t.Fatalf("setup: the writes marked done and no heads: mark=%v err=%v", mark, err)
	}
	compact(t, r.r)
	r.reopen(t)
	r.maintain(t)
	st.exchange(t, a, r)
	st.exchange(t, a, r)
	a.wantValue(t, "/only", "mine")
}
