package kvdb

import (
	"context"
	"testing"
	"time"

	cid "github.com/ipfs/go-cid"
	pb "github.com/taubyte/tau/pkg/kvdb/pb"
)

// TestAnIncarnationIsForgottenOnceExpired: a writer that goes quiet is
// forgotten a margin after what it wrote expires, and not before; what it
// wrote stays, ancient, under its own dot; and a reopen does not bring the
// entry back (T8.13: stageContext deletes).
func TestAnIncarnationIsForgottenOnceExpired(t *testing.T) {
	st := newSimTime()
	w, r := st.replica(t, "w", 0), st.replica(t, "r", 0)
	t0 := st.ms()
	put(t, w.r, "/k", "v")
	inc := w.r.replica
	r.pull(t, w)

	st.advance(simRetention + simMargin - time.Second)
	r.maintain(t)
	if !r.knows(inc) {
		t.Fatal("the writer was forgotten before its margin was up")
	}
	st.advance(time.Second)
	r.maintain(t)
	if r.knows(inc) {
		t.Fatal("the writer is still known after it expired")
	}
	r.wantValue(t, "/k", "v")
	if h := r.horizon(); h != t0 {
		t.Fatalf("the horizon is %d, want the writer's last change %d", h, t0)
	}
	if n := len(indexedBlocks(t, r.r)); n != 1 {
		t.Fatalf("after forgetting, r compacts and holds its snapshot alone; it holds %d blocks", n)
	}

	r.reopen(t)
	if r.knows(inc) {
		t.Fatal("a forgotten incarnation came back on reopen")
	}
	r.wantValue(t, "/k", "v")
}

// TestARemovalOfAnAncientElementOutlivesAStaleCopy: removing an element whose
// writer is forgotten leaves a pending removal even though the element was
// here (T8.1), and that pending removal refuses a copy of the element in a
// snapshot written before the removal, for as long as the removal is needed.
func TestARemovalOfAnAncientElementOutlivesAStaleCopy(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, w.r, "/k", "v")
	a.pull(t, w)
	b.pull(t, w)

	st.advance(simRetention + simMargin)
	st.heartbeat(t, a, b)
	a.maintain(t)
	b.maintain(t)
	if a.knows(w.r.replica) || b.knows(w.r.replica) {
		t.Fatal("setup: the writer should be forgotten")
	}
	put(t, b.r, "/x", "1")
	a.pull(t, b)
	compact(t, a.r)
	staleCopy := onlyHead(t, a.r)

	del(t, b.r, "/k")
	if n := countEntries(t, b.r, b.r.set.pendingPrefix()); n != 1 {
		t.Fatalf("removing an ancient element left %d pending removals, want 1", n)
	}
	b.mergeBlock(a, staleCopy)
	b.wantValue(t, "/k", "<deleted>")

	a.pull(t, b)
	a.wantValue(t, "/k", "<deleted>")

	st.advance(simRetention + simMargin)
	b.maintain(t)
	if n := countEntries(t, b.r, b.r.set.pendingPrefix()); n != 0 {
		t.Fatalf("an expired pending removal of an ancient element is kept: %d", n)
	}
}

// TestAnAncientElementSurvivesASnapshotThatNeverKnewItsWriter (R-S, T8.13):
// a snapshot covers only what its compactor knows, so an element of a writer
// it never knew, or forgot, is not one it removed.
func TestAnAncientElementSurvivesASnapshotThatNeverKnewItsWriter(t *testing.T) {
	st := newSimTime()
	w, a := st.replica(t, "w", 0), st.replica(t, "a", 0)
	put(t, w.r, "/k", "v")
	a.pull(t, w)
	st.advance(simRetention + simMargin)
	a.maintain(t) // forgets w and compacts: its own snapshot holds /k as ancient
	a.wantValue(t, "/k", "v")

	c := st.replica(t, "c", 0)
	put(t, c.r, "/y", "2")
	compact(t, c.r)
	a.pull(t, c)
	a.wantValue(t, "/k", "v")
	a.wantValue(t, "/y", "2")
}

// TestALateDeltaIsNotMergedOnceExpired (T8.12): a delta that arrives after its
// own expiry is not merged, so a put whose removal was forgotten cannot come
// back through it.
func TestALateDeltaIsNotMergedOnceExpired(t *testing.T) {
	st := newSimTime()
	w, b := st.replica(t, "w", 0), st.replica(t, "b", 0)
	add := put(t, w.r, "/k", "v")
	st.advance(time.Second)
	remove := del(t, w.r, "/k")

	b.mergeBlock(w, remove)
	if n := countEntries(t, b.r, b.r.set.pendingPrefix()); n != 1 {
		t.Fatalf("the removal arrived before the put: want one pending removal, have %d", n)
	}
	st.advance(simRetention + simMargin + time.Second)
	b.maintain(t)
	if n := countEntries(t, b.r, b.r.set.pendingPrefix()); n != 0 {
		t.Fatalf("the pending removal outlived its expiry: %d", n)
	}
	b.mergeBlock(w, add)
	b.wantValue(t, "/k", "<deleted>")
	if b.r.covered(dotFromPB(deltaAt(t, w, add).GetDot())) {
		t.Fatal("an expired delta was merged")
	}
}

// TestAnExpiredParentIsNeverFetched (R-P links): a link to a block nobody
// covers and whose expiry has passed is not followed: every holder has
// purged it, and fetching it would only wait for the timeout.
func TestAnExpiredParentIsNeverFetched(t *testing.T) {
	st := newSimTime()
	a, b := st.replica(t, "a", 0), st.replica(t, "b", 0)
	old := put(t, a.r, "/k", "v")
	b.pull(t, a)

	st.advance(10 * time.Second)
	a.r.mu.Lock()
	if err := a.r.drawIncarnation(); err != nil {
		t.Fatal(err)
	}
	a.r.mu.Unlock()
	put(t, a.r, "/x", "1") // links old, under a new incarnation
	b.pull(t, a)

	st.advance(simRetention + simMargin - 9*time.Second)
	// b forgets the first incarnation and purges its block, but is not
	// made to compact: its head still links the purged block.
	b.r.mu.Lock()
	if _, err := b.r.expireLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, err := b.r.purgeAncient(context.Background()); err != nil || n != 1 {
		t.Fatalf("purging the expired block: %d, %v", n, err)
	}
	b.r.mu.Unlock()
	if b.dag.holds(old) {
		t.Fatal("setup: b still holds the expired block")
	}

	f := st.replica(t, "f", 0)
	counting := newCountingDAG()
	f.dag = counting.fetchThroughDAGSvc
	f.r.dagService = counting
	counting.setRemotes(b.dag.DAGService)
	syncHeadsQuiet(f.r, b.r)
	if n := counting.take()[old]; n != 0 {
		t.Fatalf("f fetched the expired parent %d times", n)
	}
	if m := countEntries(t, f.r, f.r.missingPrefix()); m != 0 || f.r.IsDirty(context.Background()) {
		t.Fatalf("f waits for a block nobody holds: %d missing, dirty %t", m, f.r.IsDirty(context.Background()))
	}
	f.wantValue(t, "/x", "1")

	// The value the purged block wrote is still live on b; b's next
	// compaction carries it, and f gets it from there.
	b.maintain(t)
	syncHeadsQuiet(f.r, b.r)
	f.wantValue(t, "/k", "v")
}

// TestABlockFromTheFutureWaits (T8.11): a block stamped further ahead of the
// receiver's clock than the skew is not merged until the clock reaches it,
// so a clock far ahead delays its owner's writes and nothing else.
func TestABlockFromTheFutureWaits(t *testing.T) {
	st := newSimTime()
	fast, c := st.replica(t, "fast", time.Hour), st.replica(t, "c", 0)
	put(t, fast.r, "/k", "v")
	c.pull(t, fast)
	c.wantValue(t, "/k", "<deleted>")
	if c.r.IsDirty(context.Background()) || countEntries(t, c.r, c.r.retryPrefix()) != 0 {
		t.Fatal("a block from the future left a mark: a clock far ahead would fill others' stores")
	}
	// Fetched anyway, as a walk through another replica's links would.
	c.mergeBlock(fast, onlyHead(t, fast.r))
	c.wantValue(t, "/k", "<deleted>")

	st.advance(time.Hour)
	c.pull(t, fast) // heard again when fast rebroadcasts
	c.wantValue(t, "/k", "v")
}

// TestAStaleSnapshotIsRefused: a snapshot whose compactor had not heard the
// fleet since before a removal this replica has already forgotten may hold
// the removed element, and is never merged.
func TestAStaleSnapshotIsRefused(t *testing.T) {
	st := newSimTime()
	w, a, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	a.pull(t, w)
	c.pull(t, w)

	st.advance(5 * time.Second)
	del(t, a.r, "/k")
	st.advance(simRetention + simMargin)
	a.maintain(t)
	if a.knows(w.r.replica) {
		t.Fatal("setup: a should have forgotten the writer")
	}

	// c was away all along: it still holds /k and heard nothing since.
	compact(t, c.r)
	a.pull(t, c)
	a.wantValue(t, "/k", "<deleted>")
}

// TestAnOldSnapshotIsReplaced: a held block half way to its expiry is folded
// into a new snapshot, so no replica in contact ever holds only blocks a
// fresh replica would refuse to walk.
func TestAnOldSnapshotIsReplaced(t *testing.T) {
	st := newSimTime()
	a := st.replica(t, "a", 0)
	put(t, a.r, "/k", "v")
	compact(t, a.r)
	first := onlyHead(t, a.r)

	st.advance(simRetention/2 - time.Second)
	a.maintain(t)
	if onlyHead(t, a.r) != first {
		t.Fatal("a snapshot younger than half its retention was replaced")
	}
	st.advance(time.Second)
	a.maintain(t)
	second := onlyHead(t, a.r)
	if second == first || a.dag.holds(first) {
		t.Fatal("an old snapshot is neither replaced nor purged")
	}
	if n := len(indexedBlocks(t, a.r)); n != 1 {
		t.Fatalf("a quiet store holds %d blocks, want its one snapshot", n)
	}
	a.wantValue(t, "/k", "v")
}

// TestAnIdleWriterDrawsANewIncarnation (T8.3): a write after this
// incarnation was idle more than half the retention starts a new one, so it
// never writes under an incarnation its peers may have forgotten.
func TestAnIdleWriterDrawsANewIncarnation(t *testing.T) {
	st := newSimTime()
	a := st.replica(t, "a", 0)
	put(t, a.r, "/k", "1")
	first := a.r.replica
	st.advance(simRetention / 2)
	put(t, a.r, "/k", "2")
	if a.r.replica != first {
		t.Fatal("an incarnation idle for exactly half the retention was replaced")
	}
	st.advance(simRetention/2 + time.Second)
	d, prev := deltaOf(t, a.r, put(t, a.r, "/k", "3"))
	if d.replica == first || d.counter != 1 || prev != 0 {
		t.Fatalf("the write after an idle half retention is %s with prev %d, want a new incarnation", d, prev)
	}
}

// TestAWriterForgottenAndHeardAgainIsKnownFromItsPrev (T8.3): a peer that
// forgot an incarnation and then hears it again knows it from the prev of
// the delta it heard, so the chain absorbs and nothing below is claimed.
func TestAWriterForgottenAndHeardAgainIsKnownFromItsPrev(t *testing.T) {
	st := newSimTime()
	w, b := st.replica(t, "w", 0), st.replica(t, "b", 0)
	put(t, w.r, "/old", "1")
	b.pull(t, w)
	// w keeps writing, never idle long enough to retire itself; b does not
	// hear it for longer than the retention.
	var later []cid.Cid
	for range 12 {
		st.advance(simRetention / 4)
		later = append(later, put(t, w.r, "/k", "x"))
	}
	b.maintain(t)
	if b.knows(w.r.replica) {
		t.Fatal("setup: b should have forgotten w")
	}
	b.wantValue(t, "/old", "1")
	for _, c := range later[len(later)-3:] {
		b.mergeBlock(w, c)
	}
	b.r.mu.Lock()
	spans := append([]span(nil), b.r.cc.inc[w.r.replica].spans...)
	b.r.mu.Unlock()
	if len(spans) != 1 || spans[0].lo != 10 || spans[0].hi != 13 {
		t.Fatalf("b knows w as %v, want the one entry (10, 13]", spans)
	}

	// A fresh replica that heard only w's last delta knows it from 12, so
	// its snapshot does not claim /old, which b keeps.
	f := st.replica(t, "f", 0)
	f.mergeBlock(w, later[len(later)-1])
	compact(t, f.r)
	b.pull(t, f)
	b.wantValue(t, "/old", "1")
}

// TestTwoSightingsWithAGapKeepTheGap (T8.3): replicas that first saw an
// incarnation at different counters join their knowledge as two spans, and
// a dot in between is still taken when it arrives.
func TestTwoSightingsWithAGapKeepTheGap(t *testing.T) {
	st := newSimTime()
	w := st.replica(t, "w", 0)
	var cs []cid.Cid
	for i := range 20 {
		cs = append(cs, put(t, w.r, "/k"+string(rune('a'+i)), "v"))
	}
	b, c := st.replica(t, "b", 0), st.replica(t, "c", 0)
	b.mergeBlock(w, cs[9])
	c.mergeBlock(w, cs[19])
	compact(t, c.r)
	b.pull(t, c)
	b.mergeBlock(w, cs[14])
	b.wantValue(t, "/ko", "v")
}

// TestTheHorizonTravelsWithBlocks: what a replica forgot is carried by every
// block it writes, so a replica that merges one judges from it.
func TestTheHorizonTravelsWithBlocks(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, w.r, "/k", "v")
	a.pull(t, w)
	st.advance(simRetention + simMargin)
	a.maintain(t)
	if a.horizon() == 0 {
		t.Fatal("setup: a forgot nothing")
	}
	put(t, a.r, "/x", "1")
	b.pull(t, a)
	if b.horizon() != a.horizon() {
		t.Fatalf("b's horizon is %d, a's %d", b.horizon(), a.horizon())
	}
}

// TestRetentionChangesWhatIsWrittenNext: SetRetention changes the T new
// blocks carry; what was written keeps its own.
func TestRetentionChangesWhatIsWrittenNext(t *testing.T) {
	st := newSimTime()
	a := st.replica(t, "a", 0)
	first := put(t, a.r, "/k", "1")
	if err := a.r.SetRetention(simMargin); err == nil {
		t.Fatal("a retention of one margin was accepted")
	}
	if err := a.r.SetRetention(2 * simRetention); err != nil {
		t.Fatal(err)
	}
	second := put(t, a.r, "/k", "2")
	if got := deltaAt(t, a, first).GetTtl(); got != durationMillis(simRetention) {
		t.Fatalf("the first block's ttl is %d", got)
	}
	if got := deltaAt(t, a, second).GetTtl(); got != durationMillis(2*simRetention) {
		t.Fatalf("the second block's ttl is %d", got)
	}
}

func deltaAt(t testing.TB, sr *simReplica, c cid.Cid) *pb.Delta {
	t.Helper()
	_, d, err := (&crdtNodeGetter{sr.dag}).GetDelta(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestARemovalKeepsItsWritersEntry (R-C): removing an element moves its
// writer's entry to the removal's time, so a writer that went quiet long
// before is still known, and the removal refuted, for a full T after the
// removal rather than after the write.
func TestARemovalKeepsItsWritersEntry(t *testing.T) {
	st := newSimTime()
	w, a, b, c := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b, c)
	// w goes quiet; a fleet in contact carries on.
	for range int(simRetention * 8 / 10 / simRI) {
		st.advance(simRI)
		st.exchange(t, a, b, c)
	}
	del(t, a.r, "/k") // late in w's life: w's entry must now live on from here
	st.exchange(t, a, b)
	// c is away from the removal on, for less than T.
	for range int(simRetention * 7 / 10 / simRI) {
		st.advance(simRI)
		st.exchange(t, a, b)
		a.maintain(t)
		b.maintain(t)
	}
	if !a.knows(w.r.replica) {
		t.Fatal("the writer was forgotten less than T after a removal of its element")
	}
	st.exchange(t, a, b, c)
	st.exchange(t, a, b, c)
	for _, sr := range []*simReplica{a, b, c} {
		sr.wantValue(t, "/k", "<deleted>")
	}
}

// TestAPendingRemovalExpiresWhereverItTravels (T8.11): a pending removal
// expires at the time written in it, however many snapshots carry it back and
// forth between replicas that keep compacting.
func TestAPendingRemovalExpiresWhereverItTravels(t *testing.T) {
	st := newSimTime()
	w, a, b := st.replica(t, "w", 0), st.replica(t, "a", 0), st.replica(t, "b", 0)
	put(t, w.r, "/k", "v")
	st.exchange(t, w, a, b)
	st.away(t, simRetention+2*simMargin, a, b) // w is forgotten: /k is ancient
	del(t, a.r, "/k")
	st.exchange(t, a, b)
	if countEntries(t, b.r, b.r.set.pendingPrefix()) != 1 {
		t.Fatal("setup: b should keep the removal of an ancient element pending")
	}
	for range int((simRetention + 2*simMargin) / (5 * simRI)) {
		st.away(t, 5*simRI, a, b)
		compact(t, a.r)
		st.exchange(t, a, b)
		compact(t, b.r)
		st.exchange(t, a, b)
	}
	for _, sr := range []*simReplica{a, b} {
		if n := countEntries(t, sr.r, sr.r.set.pendingPrefix()); n != 0 {
			t.Fatalf("%s keeps a pending removal past its expiry after carrying it in snapshots", sr.name)
		}
	}
}

// TestASnapshotWrittenBeforeAnyContactIsJudgedByItsOldestElement (T8.8): a
// snapshot written by a replica that had heard no fleet holds that
// replica's own writes, and is only as recent as the oldest of them. Fetched
// late, after the fleet removed one and forgot the removal, it is refused,
// whatever T it was written under.
func TestASnapshotWrittenBeforeAnyContactIsJudgedByItsOldestElement(t *testing.T) {
	ctx := context.Background()
	st := newSimTime()
	a, b, c := st.replica(t, "a", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	put(t, a.r, "/x", "removed")
	st.deliver(t, a, b, c) // its write goes out, and nothing comes back
	st.advance(simRI)
	closeStore(t, a.r)
	forgetIncarnation(a.r.incarnationKey())
	a.open(t)
	if err := a.r.SetRetention(4 * simRetention); err != nil {
		t.Fatal(err)
	}
	compact(t, a.r)
	hs, _ := a.r.heads.List()
	if len(hs) != 1 {
		t.Fatalf("setup: %d heads, want the snapshot", len(hs))
	}
	snap := hs[0]
	a.out.take()
	b.r.retryLater(ctx, snap.link()) // heard, not fetched yet
	del(t, b.r, "/x")
	st.away(t, simRetention+2*simMargin, b, c)
	b.wantValue(t, "/x", "<deleted>")

	b.dag.setRemotes(a.dag.DAGService)
	if err := b.r.Repair(ctx); err != nil {
		t.Fatal(err)
	}
	if b.r.covered(snap.Dot) {
		t.Fatal("a snapshot from before any contact was merged after what it holds was removed and forgotten")
	}
	b.wantValue(t, "/x", "<deleted>")
}

// TestASnapshotCarryingStampsFromTheFutureWaits (T8.11): a replica whose
// clock ran far ahead and was put right still holds what it wrote then, and
// so does every snapshot it writes after. A correct replica merges none of
// it before its own clock gets there, so the wrong clock's stamps, and the
// incarnation they would keep known for as long, never reach its state.
func TestASnapshotCarryingStampsFromTheFutureWaits(t *testing.T) {
	st := newSimTime()
	fast, b, c := st.replica(t, "fast", 0), st.replica(t, "b", 0), st.replica(t, "c", 0)
	st.exchange(t, fast, b, c)
	fast.r.opts.Clock = st.at(1000 * simRetention)
	put(t, fast.r, "/x", "v")
	ahead := fast.incarnation()
	fast.r.opts.Clock = st.at(0)
	compact(t, fast.r)
	if at := deltaAt(t, fast, onlyHead(t, fast.r)).GetAt(); at > st.ms()+durationMillis(simS) {
		t.Fatalf("setup: the snapshot is stamped %dms ahead", at-st.ms())
	}
	st.exchange(t, fast, b, c)
	for _, sr := range []*simReplica{b, c} {
		sr.wantValue(t, "/x", "<deleted>")
		if sr.knows(ahead) {
			t.Fatalf("%s took stamps a clock far ahead wrote, from a snapshot written once it was put right", sr.name)
		}
	}
}
