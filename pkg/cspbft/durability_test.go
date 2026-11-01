package cspbft

import (
	"bytes"
	"errors"
	"testing"
)

func collect(acts []Action) []Record {
	var out []Record
	for _, a := range acts {
		if p, ok := a.(Persist); ok {
			out = append(out, p.Records...)
		}
	}
	return out
}

// checkPersistOrder is the durability contract, walked: in any one action
// slice, a vote must not be sent before the Persist that carries it.
func checkPersistOrder(t *testing.T, who ReplicaID, acts []Action) {
	t.Helper()
	durable := map[string]bool{}
	for _, a := range acts {
		switch v := a.(type) {
		case Persist:
			for _, r := range v.Records {
				if r.Kind == RecordMessage {
					durable[string(appendMessage(nil, r.Message, true))] = true
				}
			}
		case Broadcast:
			requireDurable(t, who, durable, v.Message)
		case Send:
			requireDurable(t, who, durable, v.Message)
		}
	}
}

func requireDurable(t *testing.T, who ReplicaID, durable map[string]bool, m Message) {
	t.Helper()
	switch m.Kind {
	case KindPrePrepare, KindPrepare, KindCommit, KindViewChange, KindCheckpoint, KindNewView:
	default:
		return
	}
	if !durable[string(appendMessage(nil, m, true))] {
		t.Fatalf("%s sent a %s for view %d sequence %d before the Persist that authorises it.\n\n"+
			"A vote that was sent but not remembered is exactly how a crashed replica contradicts itself, and "+
			"the contradiction is a safety failure rather than a lost message.", who, m.Kind, m.View, m.Seq)
	}
}

func TestEveryBroadcastOfAVoteIsPrecededByThePersistThatAuthorisesIt(t *testing.T) {
	for _, n := range []int{4, 7} {
		s := newSim(t, n, int64(n), withSimInterval(4))
		s.down[s.primary()] = true
		for i := uint64(1); i <= 12; i++ {
			s.request("c1", i, "p")
			s.settle(20)
		}
		if len(s.slices) < 20 {
			t.Fatalf("n=%d produced only %d action slices, which is not enough to be checking anything", n, len(s.slices))
		}
		for _, sl := range s.slices {
			checkPersistOrder(t, sl.who, sl.acts)
		}
	}
}

func TestAReplicaThatCrashedAfterPreparingDoesNotPrepareADifferentDigestAfterRestart(t *testing.T) {
	one := clientRequest("c", 1, "one")
	two := clientRequest("c", 2, "two")

	live := mkReplica(t, 4, "b")
	var journal []Record
	journal = append(journal, collect(mustStep(t, live, one))...)
	journal = append(journal, collect(mustStep(t, live, two))...)
	journal = append(journal, collect(mustStep(t, live,
		signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: one.Digest})))...)

	restarted := mkReplica(t, 4, "b")
	if _, err := restarted.Restore(journal); err != nil {
		t.Fatalf("restore: %v", err)
	}
	acts, err := restarted.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: two.Digest}))
	if !errors.Is(err, ErrEquivocation) {
		t.Fatalf("got %v, want ErrEquivocation: a replica that forgot what it prepared is a replica that will "+
			"prepare something else for the same sequence", err)
	}
	if countActions[Evidence](acts) != 1 {
		t.Fatal("the restarted replica held the memory but produced no proof")
	}
}

func TestARestoredReplicaCanStillProduceThePreparedCertificateItsViewChangeMustCarry(t *testing.T) {
	req := clientRequest("c", 1, "one")
	live := mkReplica(t, 4, "b")
	var journal []Record
	journal = append(journal, collect(mustStep(t, live, req))...)
	journal = append(journal, collect(mustStep(t, live,
		signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest})))...)
	journal = append(journal, collect(mustStep(t, live,
		signed("c", Message{Kind: KindPrepare, View: 0, Seq: 1, Digest: req.Digest})))...)
	if !prepared(live.log.at(0, 1), live.q) {
		t.Fatal("the live replica never prepared, so there is nothing to lose")
	}

	restarted := mkReplica(t, 4, "b")
	if _, err := restarted.Restore(journal); err != nil {
		t.Fatalf("restore: %v", err)
	}
	vc, ok := findBroadcast(restarted.Timeout(TimerRequest), KindViewChange)
	if !ok {
		t.Fatal("no view-change")
	}
	if len(vc.Prepared) != 1 || vc.Prepared[0].PrePrepare.Seq != 1 || vc.Prepared[0].PrePrepare.Digest != req.Digest {
		t.Fatalf("the restored replica's view-change carries %v.\n\nA view-change that omits a certificate the "+
			"replica really held lets the new view forget an assignment a quorum already made.", vc.Prepared)
	}
	if err := validateViewChange(vc, restarted.q, replicaVerifier(), restarted.known); err != nil {
		t.Fatalf("the restored certificate does not hold up for anyone else: %v", err)
	}
}

func TestARestoredReplicaDoesNotRebroadcastWhatItAlreadySent(t *testing.T) {
	req := clientRequest("c", 1, "one")
	live := mkReplica(t, 4, "b")
	var journal []Record
	journal = append(journal, collect(mustStep(t, live, req))...)
	journal = append(journal, collect(mustStep(t, live,
		signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest})))...)

	restarted := mkReplica(t, 4, "b")
	if _, err := restarted.Restore(journal); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restarted.log.at(0, 1).sentPrepare {
		t.Fatal("the restored replica does not remember having prepared, so it would prepare again")
	}
	// The same pre-prepare arriving again is a retransmission and must not
	// produce a second vote.
	acts, err := restarted.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if !errors.Is(err, ErrDuplicateVote) {
		t.Fatalf("got %v, want ErrDuplicateVote", err)
	}
	if _, ok := findBroadcast(acts, KindPrepare); ok {
		t.Fatal("a restart must not become a retransmission storm")
	}
}

func TestRestoreAfterTheFirstStepIsRefused(t *testing.T) {
	r := mkReplica(t, 4, "b")
	if _, err := r.Step(clientRequest("c", 1, "one")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(nil); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("got %v, want ErrAlreadyStarted: a journal replayed into a running replica reorders history", err)
	}
}

func TestAJournalThatContradictsItselfIsRefusedRatherThanPartlyApplied(t *testing.T) {
	r := mkReplica(t, 4, "b")
	a := signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: Digest{1}})
	b := signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: Digest{2}})
	_, err := r.Restore([]Record{rec(a), rec(b)})
	if !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("got %v, want ErrCorruptRecord", err)
	}
}

func TestARestoredViewAndCheckpointPutTheReplicaBackWhereItWas(t *testing.T) {
	r := mkReplica(t, 4, "b")
	cps := []Record{}
	for _, id := range []ReplicaID{"a", "c", "d"} {
		cps = append(cps, rec(signed(id, Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{5}})))
	}
	records := append(cps, Record{Kind: RecordCheckpointStable, Seq: 4, Digest: Digest{5}}, Record{Kind: RecordViewEntered, View: 3})
	if _, err := r.Restore(records); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if r.CurrentView() != 3 {
		t.Fatalf("view is %d, want 3: a replica that re-enters an older view votes there a second time", r.CurrentView())
	}
	low, high := r.Watermarks()
	if low != 4 || high != 12 {
		t.Fatalf("watermarks are (%d, %d], want (4, 12]", low, high)
	}
	if r.Epoch() != 4 {
		t.Fatalf("epoch is %d, want the restored checkpoint", r.Epoch())
	}
	vc, ok := findBroadcast(r.Timeout(TimerRequest), KindViewChange)
	if !ok {
		t.Fatal("no view-change")
	}
	if len(vc.Checkpoints) != 3 || vc.Seq != 4 {
		t.Fatalf("the restored view-change carries %d checkpoints for sequence %d, want the proof it was journalled with",
			len(vc.Checkpoints), vc.Seq)
	}
	if err := validateViewChange(vc, r.q, replicaVerifier(), r.known); err != nil {
		t.Fatalf("the restored proof does not hold up for anyone else: %v", err)
	}
	if !bytes.Equal(Preimage(vc.Checkpoints[0]), Preimage(signed("a", Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{5}}))) {
		t.Fatal("the proof is not the messages that were journalled")
	}
}

func TestARestartedPrimaryNeverAssignsASequenceNumberItAlreadyAssigned(t *testing.T) {
	live := mkReplica(t, 4, "a") // the primary of view 0
	one, two := clientRequest("c", 1, "one"), clientRequest("c", 2, "two")
	acts := mustStep(t, live, one)
	first, ok := findBroadcast(acts, KindPrePrepare)
	if !ok {
		t.Fatal("the primary did not assign the request it was handed")
	}

	restarted := mkReplica(t, 4, "a")
	if _, err := restarted.Restore(collect(acts)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	next, ok := findBroadcast(mustStep(t, restarted, two), KindPrePrepare)
	if !ok {
		t.Fatal("the restarted primary assigned nothing")
	}
	if next.Seq == first.Seq {
		t.Fatalf("sequence %d went to %x before the crash and to %x after it.\n\n"+
			"The two are transferable proof that a CORRECT replica equivocated, which is the one artefact the "+
			"whole quorum argument assumes cannot exist: the fault budget silently becomes f-1, and two correct "+
			"replicas that never crashed can then execute different proposals at one sequence number.",
			first.Seq, first.Digest[:4], next.Digest[:4])
	}
	if next.Seq != first.Seq+1 {
		t.Fatalf("the restarted primary continued at sequence %d, want %d", next.Seq, first.Seq+1)
	}
	if err := (Equivocation{A: first, B: next}).Verify(replicaVerifier()); err == nil {
		t.Fatal("the two assignments still verify as an equivocation against this replica")
	}
}

func TestAReplicaThatCrashedAfterAskingForANewViewDoesNotVoteInTheOldOneAgain(t *testing.T) {
	live := mkReplica(t, 4, "b")
	journal := collect(live.Timeout(TimerRequest))

	restarted := mkReplica(t, 4, "b")
	if _, err := restarted.Restore(journal); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !restarted.viewChanging || restarted.pendingView != 1 {
		t.Fatalf("after replaying its own view-change for view 1 the replica is viewChanging=%v pending=%d",
			restarted.viewChanging, restarted.pendingView)
	}
	req := clientRequest("c", 1, "one")
	mustStep(t, restarted, req)
	_, err := restarted.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if !errors.Is(err, ErrStaleView) {
		t.Fatalf("got %v, want ErrStaleView.\n\n"+
			"A VIEW-CHANGE is a promise not to vote below the view it names, and the promise is what makes the "+
			"next primary's O set complete: a certificate prepared after it went out appears in no V that "+
			"NEW-VIEW is built from, so one quorum could commit a digest the next view re-proposes differently "+
			"with nothing byzantine anywhere.", err)
	}
}

func TestARequestRestoredWithNoAssignmentBehindItCanStillBeOrdered(t *testing.T) {
	live := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	journal := collect(mustStep(t, live, req))

	restarted := mkReplica(t, 4, "b")
	if _, err := restarted.Restore(journal); err != nil {
		t.Fatalf("restore: %v", err)
	}
	acts := mustStep(t, restarted, req) // obligation 3: the client retransmits
	forwarded := false
	for _, a := range acts {
		if sd, ok := a.(Send); ok && sd.Message.Kind == KindRequest && sd.To == "a" {
			forwarded = true
		}
	}
	if !forwarded {
		t.Fatalf("the retransmission produced %d actions and no forward.\n\n"+
			"A replica that reads \"I hold the bytes\" as \"I have already dealt with it\" is neither waiting for "+
			"the request nor passing it on, and on a whole-cluster restart every replica reads it that way at "+
			"once — so the request is lost although the client is doing exactly what obligation 3 asks.", len(acts))
	}
	if countActions[SetTimer](acts) == 0 {
		t.Fatal("nothing is timing the primary out over a request this replica is now waiting for")
	}
}

func TestRestoreSaysSoWhenItComesBackWithADegradedRotation(t *testing.T) {
	r := mkReplica(t, 4, "b", WithScorer(nilScorer{}))
	var records []Record
	for _, id := range ids(4)[:Quorum(4)] {
		records = append(records, rec(signed(id, Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{1}})))
	}
	records = append(records, Record{Kind: RecordCheckpointStable, Seq: 4, Digest: Digest{1}})
	acts, err := r.Restore(records)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if countActions[Notice](acts) != 1 {
		t.Fatalf("a restore that had to discard the scorer's answer produced %d notices: a replica that comes "+
			"back with a rotation nobody else computes refuses every NEW-VIEW it is sent, and that is the one "+
			"seam the Scorer can break", countActions[Notice](acts))
	}
	if r.Primary(1) != ids(4)[1] {
		t.Fatalf("view 1 is led by %q, want the round-robin fallback", r.Primary(1))
	}
}

func TestAPrimaryAboutToContradictItselfStopsBeforeTheVoteLeaves(t *testing.T) {
	// The journal is what keeps this unreachable, so arriving here means a
	// durability contract was broken or this file has a bug. Either way the
	// second assignment must not already be in the slice the caller has been
	// told to send: the pair is transferable proof against this replica.
	r := mkReplica(t, 4, "a")
	one, two := clientRequest("c", 1, "one"), clientRequest("c", 2, "two")
	mustStep(t, r, one)
	r.lastAssigned = 0 // exactly what a restart that forgot would leave behind

	acts := mustStep(t, r, two)
	if _, ok := findBroadcast(acts, KindPrePrepare); ok {
		t.Fatal("a second assignment for one sequence number was handed to the caller to broadcast")
	}
	if countActions[Evidence](acts) != 1 {
		t.Fatalf("%d proofs handed out: the contradiction was found and then kept quiet", countActions[Evidence](acts))
	}
}
