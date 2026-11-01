package cspbft

import (
	"errors"
	"testing"
)

func TestALogIsTruncatedAtAStableCheckpointAndTheWatermarksMove(t *testing.T) {
	s := newSim(t, 4, 21, withSimInterval(4))
	for i := uint64(1); i <= 9; i++ {
		s.request("c1", i, "p")
		s.settle(10)
	}
	for _, id := range s.ids {
		r := s.rep[id]
		low, high := r.Watermarks()
		if low != 8 {
			t.Fatalf("%s: low water mark is %d, want the last stable checkpoint at 8", id, low)
		}
		if high != 16 {
			t.Fatalf("%s: high water mark is %d, want low + 2K", id, high)
		}
		for k := range r.log.entries {
			if k.seq <= low {
				t.Fatalf("%s still holds sequence %d below the stable checkpoint: the log is bounded by discarding, "+
					"and a log that is never truncated grows without bound", id, k.seq)
			}
		}
		if r.Epoch() != 8 {
			t.Fatalf("%s: epoch is %d, want the stable checkpoint", id, r.Epoch())
		}
	}
}

func TestACheckpointBelowTheStableOneOrTooFarAheadIsRefused(t *testing.T) {
	r := mkReplica(t, 4, "b")
	r.lastStable = 8
	_, err := r.Step(signed("a", Message{Kind: KindCheckpoint, Seq: 8, Digest: Digest{1}}))
	if !errors.Is(err, ErrOutOfWatermarks) {
		t.Fatalf("got %v, want ErrOutOfWatermarks for a checkpoint at the stable one", err)
	}
	_, err = r.Step(signed("a", Message{Kind: KindCheckpoint, Seq: 9999, Digest: Digest{1}}))
	if !errors.Is(err, ErrOutOfWatermarks) {
		t.Fatalf("got %v, want ErrOutOfWatermarks for one far ahead", err)
	}
	if _, err := r.Checkpointed(0, Digest{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed for a checkpoint at sequence zero", err)
	}
	if _, err := r.Checkpointed(4, Digest{}); !errors.Is(err, ErrOutOfWatermarks) {
		t.Fatalf("got %v, want ErrOutOfWatermarks for a checkpoint the replica has already passed", err)
	}
}

func TestACheckpointIsNotStableUntilAQuorumSaysTheSameThing(t *testing.T) {
	r := mkReplica(t, 4, "b") // q = 3
	acts, err := r.Checkpointed(4, Digest{9})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findBroadcast(acts, KindCheckpoint); !ok {
		t.Fatal("Checkpointed must publish what this replica computed")
	}
	if r.lastStable != 0 {
		t.Fatal("one replica's own digest made a checkpoint stable")
	}
	mustStep(t, r, signed("a", Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{9}}))
	if r.lastStable != 0 {
		t.Fatal("two agreeing replicas made a checkpoint stable at q=3")
	}
	// A third replica disagreeing about the state must not complete the
	// quorum for the digest the other two hold.
	mustStep(t, r, signed("c", Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{7}}))
	if r.lastStable != 0 {
		t.Fatal("a checkpoint for a different state was counted toward this one")
	}
	mustStep(t, r, signed("d", Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{9}}))
	if r.lastStable != 4 {
		t.Fatalf("lastStable is %d, want 4 once a quorum certified one state", r.lastStable)
	}
}

func TestAReplicaThatMissedAnEpochIsToldToFetchTheStateAQuorumCertified(t *testing.T) {
	s := newSim(t, 4, 31, withSimInterval(4))
	// One replica hears nothing for long enough that its log window closes
	// behind it. It cannot catch up by replaying the order, because the order
	// below the new low water mark has been discarded everywhere.
	s.part["r03"] = true
	for i := uint64(1); i <= 9; i++ {
		s.request("c1", i, "p")
		s.settle(10)
	}
	if s.lastExec["r03"] != 0 {
		t.Fatalf("the partitioned replica executed %d, so it was not actually cut off", s.lastExec["r03"])
	}
	s.part["r03"] = false
	// Everything it missed is gone from the network, so only a state transfer
	// can put it back.
	var sawFetch bool
	for _, sl := range s.slices {
		if countActions[FetchState](sl.acts) > 0 {
			sawFetch = true
		}
	}
	for i := uint64(10); i <= 13; i++ {
		s.request("c1", i, "p")
		s.settle(20)
	}
	for _, sl := range s.slices {
		if countActions[FetchState](sl.acts) > 0 {
			sawFetch = true
		}
	}
	if !sawFetch {
		t.Fatal("a replica that fell behind a stable checkpoint was never told to fetch the state")
	}
	if s.lastExec["r03"] < 12 {
		t.Fatalf("the rejoining replica is at %d, want to have caught up", s.lastExec["r03"])
	}
	s.checkSafety()
}

func TestInstalledIsRefusedWhenItDigestsToSomethingTheQuorumDidNotCertify(t *testing.T) {
	r := mkReplica(t, 4, "b")
	r.lastStable = 4
	r.stableDigest = Digest{9}
	if _, err := r.Installed(4, Digest{8}); !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("got %v, want ErrBadCertificate: installing a state a quorum did not certify is installing a fork", err)
	}
	if acts, err := r.Installed(4, Digest{9}); err != nil || len(acts) != 0 {
		t.Fatalf("installing the state it already has must be a no-op, got %v %v", acts, err)
	}
	if _, err := r.Installed(6, Digest{9}); err != nil {
		t.Fatalf("installing a later state: %v", err)
	}
	if r.LastExecuted() != 6 {
		t.Fatalf("LastExecuted is %d after a transfer to 6", r.LastExecuted())
	}
}

func TestAPrimaryOrdersARetransmissionItsOwnCheckpointMadeItForget(t *testing.T) {
	// The memory of what was executed must end where every replica's ends, or a primary
	// drops a retransmission a transferred backup still holds outstanding.
	p := mkReplica(t, 4, "a") // the primary of view 0
	req := clientRequest("c", 1, "one")
	p.executedAt[req.Digest] = 3
	if _, ok := findBroadcast(mustStep(t, p, req), KindPrePrepare); ok {
		t.Fatal("a request executed since the last checkpoint was ordered a second time")
	}
	p.gcRequests(4) // the checkpoint at 4 becomes stable
	acts := mustStep(t, p, req)
	if _, ok := findBroadcast(acts, KindPrePrepare); !ok {
		t.Fatalf("the retransmission produced %d actions and no assignment, although every replica has now "+
			"forgotten the execution at the same point", len(acts))
	}
}

func TestAPayloadIsStoredWhenAnAssignmentIsAlreadyWaitingForIt(t *testing.T) {
	// A new primary legitimately re-proposes at a second sequence a request
	// this replica executed and checkpointed away. Refuse the payload and the
	// held assignment waits for bytes that are never stored, so execution
	// stops at the gap until the next state transfer.
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	r.executedAt[req.Digest] = 1
	if _, err := r.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 2, Digest: req.Digest})); err != nil {
		t.Fatalf("pre-prepare: %v", err)
	}
	if len(r.orphans[req.Digest]) != 1 {
		t.Fatal("the assignment was not held for its missing payload")
	}
	acts := mustStep(t, r, req)
	if _, ok := findBroadcast(acts, KindPrepare); !ok {
		t.Fatalf("the payload produced %d actions and no prepare: the assignment is still waiting for it", len(acts))
	}
	if len(r.orphans[req.Digest]) != 0 || r.orphansHeld != 0 {
		t.Fatalf("%d assignments are still held", r.orphansHeld)
	}
}

func TestAStateTransferEndsTheMemoryOfWhatWasExecutedBelowIt(t *testing.T) {
	r := mkReplica(t, 4, "b")
	early, late := Digest{1}, Digest{2}
	r.executedAt[early], r.executedAt[late] = 2, 9
	if _, err := r.Installed(4, Digest{7}); err != nil {
		t.Fatalf("installed: %v", err)
	}
	if _, still := r.executedAt[early]; still {
		t.Fatal("a request executed below the transferred state is still remembered here, so its retransmission " +
			"would be dropped by this replica and held outstanding by one that transferred the same state")
	}
	if _, gone := r.executedAt[late]; !gone {
		t.Fatal("a request executed above the transferred state was forgotten")
	}
	if r.LastExecuted() != 4 {
		t.Fatalf("last executed is %d, want the transferred point", r.LastExecuted())
	}
}
