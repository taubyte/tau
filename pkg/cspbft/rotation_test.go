package cspbft

import (
	"errors"
	"math/rand"
	"testing"
)

type reverseScorer struct{ calls int }

func (s *reverseScorer) Rank(sn Snapshot) []ReplicaID {
	s.calls++
	out := make([]ReplicaID, 0, len(sn.Replicas))
	for i := len(sn.Replicas) - 1; i >= 0; i-- {
		out = append(out, sn.Replicas[i])
	}
	return out
}

type fixedScorer struct{ order []ReplicaID }

func (s fixedScorer) Rank(Snapshot) []ReplicaID { return s.order }

type panicScorer struct{}

func (panicScorer) Rank(Snapshot) []ReplicaID { panic("a scorer that read a nil map") }

type nilScorer struct{}

func (nilScorer) Rank(Snapshot) []ReplicaID { return nil }

type sortingScorer struct{}

// Rank sorts the slice it was handed, in place, which is the mistake a Scorer
// written against a slice header will make.
func (sortingScorer) Rank(sn Snapshot) []ReplicaID {
	for i := 0; i < len(sn.Replicas)/2; i++ {
		j := len(sn.Replicas) - 1 - i
		sn.Replicas[i], sn.Replicas[j] = sn.Replicas[j], sn.Replicas[i]
	}
	return sn.Replicas
}

func TestTheDefaultRotationIsTextbookRoundRobin(t *testing.T) {
	r := mkReplica(t, 4, "a")
	for v := View(0); v < 9; v++ {
		want := ids(4)[uint64(v)%4]
		if got := r.Primary(v); got != want {
			t.Fatalf("view %d: primary %q, want %q", v, got, want)
		}
	}
	if got := (RoundRobin{}).Rank(Snapshot{Replicas: ids(4)}); len(got) != 4 || got[0] != "a" {
		t.Fatalf("RoundRobin must be the identity permutation, got %v", got)
	}
}

func TestAScorerReordersTheRotationAndEveryReplicaStillTakesItsTurn(t *testing.T) {
	sc := &reverseScorer{}
	r := mkReplica(t, 4, "a", WithScorer(sc))
	if err := r.rot.recompute(4, Digest{1}); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	seen := map[ReplicaID]bool{}
	for v := View(0); v < 4; v++ {
		seen[r.Primary(v)] = true
	}
	if len(seen) != 4 {
		t.Fatalf("one cycle of views visited %d replicas, want all four: the fairness bound comes from every replica taking its turn", len(seen))
	}
	if r.Primary(0) != "d" {
		t.Fatalf("the scorer's order was not used: primary(0) = %q", r.Primary(0))
	}
}

func TestAScorerThatPanicsFallsBackToRoundRobin(t *testing.T) {
	r := mkReplica(t, 4, "a", WithScorer(panicScorer{}))
	err := r.rot.recompute(4, Digest{1})
	if !errors.Is(err, ErrScorerPermutation) {
		t.Fatalf("got %v, want ErrScorerPermutation", err)
	}
	for v := View(0); v < 4; v++ {
		if r.Primary(v) != ids(4)[uint64(v)%4] {
			t.Fatal("a panicking scorer must cost the protocol its scoring and not its liveness")
		}
	}
}

func TestAScorerReturningSomethingThatIsNotAPermutationIsRejectedBeforeItReachesTheRotation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []ReplicaID
	}{
		{"a duplicate", []ReplicaID{"a", "b", "b", "c"}},
		{"too short", []ReplicaID{"a", "b", "c"}},
		{"an unknown id", []ReplicaID{"a", "b", "c", "zz"}},
		{"nothing at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mkReplica(t, 4, "a", WithScorer(fixedScorer{order: tc.order}))
			if err := r.rot.recompute(4, Digest{1}); !errors.Is(err, ErrScorerPermutation) {
				t.Fatalf("got %v, want ErrScorerPermutation", err)
			}
			if r.Primary(1) != "b" {
				t.Fatalf("the fallback is the identity permutation, got primary(1) = %q", r.Primary(1))
			}
		})
	}
	r := mkReplica(t, 4, "a", WithScorer(nilScorer{}))
	if err := r.rot.recompute(4, Digest{1}); !errors.Is(err, ErrScorerPermutation) {
		t.Fatalf("got %v, want ErrScorerPermutation for a nil return", err)
	}
}

func TestAScorerCannotReorderTheConfigurationItWasHandedAMongst(t *testing.T) {
	r := mkReplica(t, 4, "a", WithScorer(sortingScorer{}))
	if err := r.rot.recompute(4, Digest{1}); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	// The configuration itself — the thing every quorum index is against —
	// must be untouched by a Scorer that sorted what it was given.
	want := ids(4)
	for i := range want {
		if r.replicas[i] != want[i] {
			t.Fatalf("the configuration is now %v: a scorer reordered it, and every quorum index with it", r.replicas)
		}
	}
}

func TestTheRotationIsFrozenBetweenStableCheckpoints(t *testing.T) {
	sc := &reverseScorer{}
	r := mkReplica(t, 4, "a", WithScorer(sc))
	if sc.calls != 0 {
		t.Fatal("a scorer must not be consulted before any state is agreed")
	}
	before := make([]ReplicaID, 4)
	for v := View(0); v < 4; v++ {
		before[v] = r.Primary(v)
	}
	for v := View(0); v < 20; v++ {
		if r.Primary(v) != before[v%4] {
			t.Fatalf("view %d led by %q, and by %q one cycle earlier: the rotation moved without a stable checkpoint",
				v, r.Primary(v), before[v%4])
		}
	}
	if sc.calls != 0 {
		t.Fatalf("the scorer was consulted %d times without a checkpoint: between checkpoints the rotation is frozen, "+
			"which is what makes two correct replicas in one view agree about who leads", sc.calls)
	}
	if err := r.rot.recompute(4, Digest{9}); err != nil {
		t.Fatal(err)
	}
	if sc.calls != 1 || r.Primary(0) == before[0] {
		t.Fatalf("a stable checkpoint must move the rotation exactly once: calls=%d, view 0 still led by %q", sc.calls, before[0])
	}
	if r.Epoch() != 4 {
		t.Fatalf("epoch is %d, want the checkpoint it is bound to", r.Epoch())
	}
}

func TestTheRotationDigestBindsThePermutationToItsEpoch(t *testing.T) {
	a := mkReplica(t, 4, "a", WithScorer(&reverseScorer{}))
	b := mkReplica(t, 4, "b", WithScorer(&reverseScorer{}))
	if a.rot.digest != b.rot.digest {
		t.Fatal("two replicas at the same epoch computed different rotation digests")
	}
	if err := a.rot.recompute(4, Digest{1}); err != nil {
		t.Fatal(err)
	}
	if err := b.rot.recompute(8, Digest{1}); err != nil {
		t.Fatal(err)
	}
	if a.rot.digest == b.rot.digest {
		t.Fatal("the same permutation at two epochs produced one digest: a new-view could then claim a rotation from a different checkpoint")
	}
}

func TestABackupRejectsANewViewWhoseRotationItDoesNotAgreeWith(t *testing.T) {
	r := mkReplica(t, 4, "b")
	nv := signed("b", Message{
		Kind: KindNewView, View: 1, Epoch: 99, RotationDigest: Digest{7},
		ViewChanges: []Message{signed("a", Message{Kind: KindViewChange, View: 1})},
		PrePrepares: nil,
	})
	_, err := r.Step(nv)
	if !errors.Is(err, ErrRotationMismatch) {
		t.Fatalf("got %v, want ErrRotationMismatch: a disagreement about who leads must have a name rather than being experienced as a stall", err)
	}
}

func TestAFaultyReplicaIsPrimaryForAtMostTwoFConsecutiveViewsAcrossAnEpochBoundary(t *testing.T) {
	for n := 4; n <= 16; n++ {
		f := Faults(n)
		set := ids(n)
		rnd := rand.New(rand.NewSource(int64(n)))
		for trial := 0; trial < 300; trial++ {
			p1 := shuffled(set, rnd)
			p2 := shuffled(set, rnd)
			faulty := map[ReplicaID]bool{}
			for _, id := range shuffled(set, rnd)[:f] {
				faulty[id] = true
			}
			boundary := View(rnd.Intn(4 * n))
			run, longest := 0, 0
			for v := View(0); v < View(8*n); v++ {
				perm := p1
				if v >= boundary {
					perm = p2
				}
				if faulty[perm[uint64(v)%uint64(n)]] {
					run++
					if run > longest {
						longest = run
					}
				} else {
					run = 0
				}
			}
			if longest > 2*f {
				t.Fatalf("n=%d f=%d: %d consecutive faulty primaries across an epoch boundary", n, f, longest)
			}
		}
	}
}

func TestTheTwoFBoundIsTightAndThereforeWorthNaming(t *testing.T) {
	// f=1, n=4: the replica last in one epoch's permutation and first in the
	// next is primary for two views running. This is the whole cost of the
	// scoring layer, and it is reached rather than merely permitted — so it is
	// reached here, through Primary, across a real recompute.
	r := mkReplica(t, 4, "a", WithScorer(favourScorer{first: "d"}))
	last := r.Primary(3)
	if err := r.rot.recompute(4, Digest{1}); err != nil {
		t.Fatal(err)
	}
	if first := r.Primary(4); first != last {
		t.Fatalf("view 3 was led by %q and view 4 by %q: the bound this package names as its cost is not reached, "+
			"so either the rotation does not move at a checkpoint or the doc is claiming a cost it does not pay", last, first)
	}
	if last != "d" {
		t.Fatalf("the epoch boundary handed two consecutive views to %q, want the replica the scorer moved", last)
	}
}

func shuffled(in []ReplicaID, rnd *rand.Rand) []ReplicaID {
	out := append([]ReplicaID(nil), in...)
	rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
