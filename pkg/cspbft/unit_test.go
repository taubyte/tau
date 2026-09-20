package cspbft

import (
	"errors"
	"testing"
	"time"
)

// unit_test.go drives one replica directly, so that a single acceptance rule
// can be stated as a property and checked without a network.

func ids(n int) []ReplicaID {
	out := make([]ReplicaID, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ReplicaID(string(rune('a'+i))))
	}
	return out
}

func mkReplica(t *testing.T, n int, self ReplicaID, opts ...Option) *Replica {
	t.Helper()
	base := []Option{
		WithSigner(signAs(self)),
		WithVerifier(replicaVerifier()),
		WithClientVerifier(clientVerifier()),
		WithCheckpointInterval(4),
	}
	r, err := New(ids(n), self, append(base, opts...)...)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return r
}

func signed(from ReplicaID, m Message) Message {
	m.From = from
	m.Signature = sigFor("replica", string(from), Preimage(m))
	return m
}

func clientRequest(c ClientID, serial uint64, payload string) Message {
	m := Message{Kind: KindRequest, Client: c, Serial: serial, Proposal: Proposal(payload)}
	m.Digest = RequestDigest(SHA256, c, serial, m.Proposal)
	m.Signature = sigFor("client", string(c), Preimage(m))
	return m
}

func mustStep(t *testing.T, r *Replica, m Message) []Action {
	t.Helper()
	acts, err := r.Step(m)
	if err != nil {
		t.Fatalf("step %s: %v", m.Kind, err)
	}
	return acts
}

func countActions[T Action](acts []Action) int {
	n := 0
	for _, a := range acts {
		if _, ok := a.(T); ok {
			n++
		}
	}
	return n
}

func findBroadcast(acts []Action, k Kind) (Message, bool) {
	for _, a := range acts {
		if b, ok := a.(Broadcast); ok && b.Message.Kind == k {
			return b.Message, true
		}
	}
	return Message{}, false
}

// --- quorum arithmetic ----------------------------------------------------

func TestQuorumIntersectsInACorrectReplicaForEveryNUpToOneHundred(t *testing.T) {
	for n := 4; n <= 100; n++ {
		f := Faults(n)
		q := Quorum(n)
		// Two quorums overlap in 2q-n replicas; at most f of those can be
		// faulty, so the overlap must contain at least one correct replica.
		if 2*q-n <= f {
			t.Fatalf("n=%d f=%d q=%d: two quorums can overlap in %d replicas, all of which could be faulty", n, f, q, 2*q-n)
		}
		// And a quorum must be reachable when f replicas are silent.
		if q > n-f {
			t.Fatalf("n=%d f=%d q=%d: a quorum is unreachable with %d replicas silent", n, f, q, f)
		}
	}
}

func TestAtThreeFPlusOneTheQuorumIsTheFamiliarTwoFPlusOne(t *testing.T) {
	for f := 1; f <= 20; f++ {
		n := 3*f + 1
		if got := Faults(n); got != f {
			t.Fatalf("Faults(%d) = %d, want %d", n, got, f)
		}
		if got, want := Quorum(n), 2*f+1; got != want {
			t.Fatalf("Quorum(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestAConfigurationOfThreeReplicasIsRefusedBecauseItToleratesNoFaults(t *testing.T) {
	_, err := New(ids(3), "a", WithSigner(signAs("a")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier()))
	if !errors.Is(err, ErrTooFewReplicas) {
		t.Fatalf("got %v, want ErrTooFewReplicas", err)
	}
	if Faults(3) != 0 || Faults(0) != 0 {
		t.Fatal("three replicas tolerate no faults, and neither do none")
	}
}

func TestADuplicateReplicaIdIsRefusedRatherThanDeduped(t *testing.T) {
	set := []ReplicaID{"a", "b", "c", "d", "b"}
	_, err := New(set, "a", WithSigner(signAs("a")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier()))
	if !errors.Is(err, ErrDuplicateReplica) {
		t.Fatalf("got %v, want ErrDuplicateReplica: a set built from two sources that silently dedupes "+
			"leaves the caller with a quorum one replica short of what it believes", err)
	}
}

func TestAReplicaThatIsNotInItsOwnConfigurationIsRefused(t *testing.T) {
	_, err := New(ids(4), "z", WithSigner(signAs("z")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier()))
	if !errors.Is(err, ErrSelfNotInSet) {
		t.Fatalf("got %v, want ErrSelfNotInSet", err)
	}
}

func TestAReplicaWithoutTheThreeThingsItCannotWorkWithoutIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		want error
	}{
		{"no signer", []Option{WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier())}, ErrNoSigner},
		{"no verifier", []Option{WithSigner(signAs("a")), WithClientVerifier(clientVerifier())}, ErrNoVerifier},
		{"no client verifier", []Option{WithSigner(signAs("a")), WithVerifier(replicaVerifier())}, ErrNoClientVerifier},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(ids(4), "a", tc.opts...); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	for _, opt := range []Option{WithSigner(nil), WithVerifier(nil), WithClientVerifier(nil), WithHasher(nil), WithScorer(nil)} {
		if _, err := New(ids(4), "a", WithSigner(signAs("a")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier()), opt); err == nil {
			t.Fatal("a nil dependency was accepted")
		}
	}
}

func TestAnOptionValidatesItsOwnArgumentRatherThanFailingOnTheFirstVote(t *testing.T) {
	base := []Option{WithSigner(signAs("a")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier())}
	for _, tc := range []struct {
		name string
		opt  Option
		want error
	}{
		{"interval zero", WithCheckpointInterval(0), ErrInvalidInterval},
		{"base timeout zero", WithBaseTimeout(0), ErrInvalidTimeout},
		{"max timeout zero", WithMaxTimeout(0), ErrInvalidTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(ids(4), "a", append(base, tc.opt)...); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	_, err := New(ids(4), "a", append(base, WithBaseTimeout(time.Minute), WithMaxTimeout(time.Second))...)
	if !errors.Is(err, ErrInvalidTimeout) {
		t.Fatalf("got %v: a ceiling below the base would make the first doubling shorten the wait", err)
	}
}

func TestAdviceNamesTheCostOfAConfigurationThatBuysNoExtraFaultTolerance(t *testing.T) {
	if got := Advice(3); len(got) != 1 {
		t.Fatalf("Advice(3) = %v, want one sentence saying it tolerates nothing", got)
	}
	six := Advice(6)
	if len(six) != 2 {
		t.Fatalf("Advice(6) = %v, want the warning that it pays a larger quorum for the same one fault", six)
	}
	if len(Advice(4)) != 1 {
		t.Fatalf("Advice(4) = %v, want no warning: four is 3f+1", Advice(4))
	}
	if len(Advice(100)) != 2 {
		t.Fatalf("Advice(100) = %v, want the supported-range warning too", Advice(100))
	}
	if len(Advice(102)) != 3 {
		t.Fatalf("Advice(102) = %v, want both warnings", Advice(102))
	}
}

func TestTheReplicaSetIsCanonicalisedSoTwoReplicasAgreeOnThePrimary(t *testing.T) {
	backward := []ReplicaID{"d", "c", "b", "a"}
	ra := mkReplica(t, 4, "a") // built from the ascending set
	rb, err := New(backward, "a", WithSigner(signAs("a")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier()))
	if err != nil {
		t.Fatal(err)
	}
	for v := View(0); v < 12; v++ {
		if ra.Primary(v) != rb.Primary(v) {
			t.Fatalf("view %d: %q vs %q — v mod n must be over a canonical order", v, ra.Primary(v), rb.Primary(v))
		}
	}
}

// --- acceptance rules -----------------------------------------------------

func TestABackupRefusesASecondPrePrepareForTheSameViewAndSequenceWithADifferentDigest(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	other := clientRequest("c", 2, "two")
	mustStep(t, r, req)
	mustStep(t, r, other)

	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	acts, err := r.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: other.Digest}))
	if !errors.Is(err, ErrEquivocation) {
		t.Fatalf("got %v, want ErrEquivocation", err)
	}
	if countActions[Evidence](acts) != 1 {
		t.Fatalf("a contradiction must produce transferable proof, got %v", acts)
	}
	proof := acts[0].(Evidence).Proof
	if err := proof.Verify(replicaVerifier()); err != nil {
		t.Fatalf("the proof does not verify for a third party: %v", err)
	}
	if proof.Offender() != "a" {
		t.Fatalf("the proof incriminates %q", proof.Offender())
	}
}

func TestTwoPreparesFromTheSameReplicaCountAsOne(t *testing.T) {
	// Seven replicas, so a certificate needs four prepares and one sender
	// repeating itself is visibly short rather than accidentally enough.
	r := mkReplica(t, 7, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))

	p := signed("c", Message{Kind: KindPrepare, View: 0, Seq: 1, Digest: req.Digest})
	mustStep(t, r, p)
	_, err := r.Step(p)
	if !errors.Is(err, ErrDuplicateVote) {
		t.Fatalf("got %v, want ErrDuplicateVote", err)
	}
	e := r.log.at(0, 1)
	if len(e.prepares) != 2 {
		t.Fatalf("the log holds %d prepares, want 2 (this replica's and c's)", len(e.prepares))
	}
	if prepared(e, r.q) {
		t.Fatal("one sender's two prepares made a certificate: S8 is what stops one replica filling a quorum")
	}
}

func TestAPreparedCertificateIsTwoFPreparesPlusThePrePrepare(t *testing.T) {
	r := mkReplica(t, 4, "b") // f=1, q=3, so prepared needs the pre-prepare and 2 prepares
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	acts := mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if _, ok := findBroadcast(acts, KindPrepare); !ok {
		t.Fatal("a backup that accepted a pre-prepare must prepare")
	}
	e := r.log.at(0, 1)
	if prepared(e, r.q) {
		t.Fatal("its own prepare alone made a certificate")
	}
	acts = mustStep(t, r, signed("c", Message{Kind: KindPrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if !prepared(e, r.q) {
		t.Fatalf("2 prepares (its own and c's) plus the pre-prepare is prepared at f=%d", r.f)
	}
	if _, ok := findBroadcast(acts, KindCommit); !ok {
		t.Fatal("prepared must move the replica to the commit phase")
	}
}

func TestThePrimarysOwnPrepareIsNotCountedTowardItsOwnPrePrepare(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	// "a" is the primary of view 0 and has no business sending a prepare; if
	// it did and it counted, a faulty primary would supply two of three votes.
	mustStep(t, r, signed("a", Message{Kind: KindPrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if prepared(r.log.at(0, 1), r.q) {
		t.Fatal("the primary's own prepare was counted toward its own assignment")
	}
}

func TestACommitCertificateCountsTheReplicasOwnCommit(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	mustStep(t, r, signed("c", Message{Kind: KindPrepare, View: 0, Seq: 1, Digest: req.Digest}))
	e := r.log.at(0, 1)
	if len(e.commits) != 1 {
		t.Fatalf("after preparing, the replica holds %d commits, want its own", len(e.commits))
	}
	mustStep(t, r, signed("a", Message{Kind: KindCommit, View: 0, Seq: 1, Digest: req.Digest}))
	if committedLocal(e, r.q) {
		t.Fatal("two commits made a decision at q=3")
	}
	acts := mustStep(t, r, signed("c", Message{Kind: KindCommit, View: 0, Seq: 1, Digest: req.Digest}))
	if !committedLocal(e, r.q) {
		t.Fatal("three commits including its own is committed-local")
	}
	if countActions[Execute](acts) != 1 {
		t.Fatalf("committed-local at the next sequence must execute, got %v", acts)
	}
}

func TestASequenceNumberAboveTheHighWaterMarkIsRejected(t *testing.T) {
	r := mkReplica(t, 4, "b") // K=4, so L=8 and the window is (0, 8]
	low, high := r.Watermarks()
	if low != 0 || high != 8 {
		t.Fatalf("watermarks are (%d, %d], want (0, 8]", low, high)
	}
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	_, err := r.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 9, Digest: req.Digest}))
	if !errors.Is(err, ErrOutOfWatermarks) {
		t.Fatalf("got %v, want ErrOutOfWatermarks: the window is what stops a faulty primary exhausting the sequence space", err)
	}
	_, err = r.Step(signed("c", Message{Kind: KindPrepare, View: 0, Seq: 900, Digest: req.Digest}))
	if !errors.Is(err, ErrOutOfWatermarks) {
		t.Fatalf("got %v, want ErrOutOfWatermarks for a prepare too", err)
	}
	_, err = r.Step(signed("c", Message{Kind: KindCommit, View: 0, Seq: 900, Digest: req.Digest}))
	if !errors.Is(err, ErrOutOfWatermarks) {
		t.Fatalf("got %v, want ErrOutOfWatermarks for a commit too", err)
	}
}

func TestAPrePrepareFromAReplicaThatIsNotThePrimaryIsRefused(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	_, err := r.Step(signed("d", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if !errors.Is(err, ErrNotPrimary) {
		t.Fatalf("got %v, want ErrNotPrimary", err)
	}
}

func TestAMessageFromOutsideTheConfigurationIsRefusedWithoutBlamingItsSender(t *testing.T) {
	r := mkReplica(t, 4, "b")
	_, err := r.Step(signed("zz", Message{Kind: KindPrepare, View: 0, Seq: 1}))
	if !errors.Is(err, ErrNotInConfiguration) {
		t.Fatalf("got %v, want ErrNotInConfiguration", err)
	}
	if errors.Is(err, ErrMalformed) || errors.Is(err, ErrBadSignature) {
		t.Fatal("a stale peer list is not the sender's fault and must not read as one")
	}
}

func TestAForgedSignatureNeverReachesTheLog(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	forged := signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest})
	forged.Signature = []byte("not a signature")
	_, err := r.Step(forged)
	if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
	if r.log.at(0, 1) != nil {
		t.Fatal("an unverified message reached the log: one forged packet would then be a permanent accusation against a correct replica")
	}
	bad := clientRequest("c", 2, "two")
	bad.Signature = []byte("nope")
	if _, err := r.Step(bad); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature for a client too", err)
	}
}

func TestARequestWhoseDigestIsNotTheDigestOfItsContentIsRefused(t *testing.T) {
	r := mkReplica(t, 4, "b")
	m := Message{Kind: KindRequest, Client: "c", Serial: 1, Proposal: Proposal("one")}
	m.Digest = RequestDigest(SHA256, "c", 1, Proposal("something else"))
	m.Signature = sigFor("client", "c", Preimage(m))
	_, err := r.Step(m)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v, want ErrMalformed: a request ordered under one name and executed under another is not one request", err)
	}
}

func TestAMessageFromAnOlderViewIsStaleAndOneFromAFutureViewSaysThisReplicaIsBehind(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	_, err := r.Step(signed("c", Message{Kind: KindPrepare, View: 3, Seq: 1, Digest: req.Digest}))
	if !errors.Is(err, ErrFutureView) {
		t.Fatalf("got %v, want ErrFutureView", err)
	}
	r.view = 5
	_, err = r.Step(signed("c", Message{Kind: KindPrepare, View: 3, Seq: 1, Digest: req.Digest}))
	if !errors.Is(err, ErrStaleView) {
		t.Fatalf("got %v, want ErrStaleView", err)
	}
}

func TestAPrePrepareWhosePayloadHasNotArrivedIsHeldAndAcceptedWhenItDoes(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	acts, err := r.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if err != nil || len(acts) != 0 {
		t.Fatalf("reordering is not a fault: got %v, %v", acts, err)
	}
	if r.log.at(0, 1) != nil {
		t.Fatal("an assignment whose payload is unknown must not be voted on yet")
	}
	acts = mustStep(t, r, req)
	if _, ok := findBroadcast(acts, KindPrepare); !ok {
		t.Fatalf("the held assignment was not taken up when its payload arrived: %v", acts)
	}
}

func TestAnUnknownTimerIsANoticeAndNotAViewChange(t *testing.T) {
	r := mkReplica(t, 4, "b")
	acts := r.Timeout(Timer(99))
	if len(acts) != 1 {
		t.Fatalf("got %v", acts)
	}
	n, ok := acts[0].(Notice)
	if !ok || !errors.Is(n.Err, ErrUnknownTimer) {
		t.Fatalf("got %v, want a Notice carrying ErrUnknownTimer", acts[0])
	}
	if r.CurrentView() != 0 {
		t.Fatal("an unknown timer moved the view")
	}
	if Timer(99).String() != "Timer(99)" || TimerRequest.String() != "request" || TimerNewView.String() != "new-view" {
		t.Fatal("timers must name themselves for a log line")
	}
}

func TestASignerThatFailsStopsTheReplicaRatherThanLettingItVoteSilently(t *testing.T) {
	boom := errors.New("hsm is on fire")
	r := mkReplica(t, 4, "b", WithSigner(func([]byte) ([]byte, error) { return nil, boom }))
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	_, err := r.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the signer's error", err)
	}
	if _, err := r.Step(req); !errors.Is(err, boom) {
		t.Fatal("a replica that cannot sign must refuse every further input rather than pretend")
	}
	if acts := r.Timeout(TimerRequest); len(acts) != 1 {
		t.Fatalf("got %v, want one Notice", acts)
	}
	if _, err := r.Checkpointed(4, Digest{}); !errors.Is(err, boom) {
		t.Fatal("Checkpointed must refuse too")
	}
	if _, err := r.Installed(4, Digest{}); !errors.Is(err, boom) {
		t.Fatal("Installed must refuse too")
	}
}

func TestASecondAssignmentIsProvenEvenWhenThePayloadBehindItNeverArrives(t *testing.T) {
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))

	acts, err := r.Step(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: Digest{9}}))
	if !errors.Is(err, ErrEquivocation) {
		t.Fatalf("got %v, want ErrEquivocation.\n\n"+
			"A second assignment is a contradiction whether or not the payload behind it is here. Held until the "+
			"bytes arrive, the proof depends on the network delivering a request this replica has no other use "+
			"for — and the primary that equivocated is the one deciding whether it ever does.", err)
	}
	if countActions[Evidence](acts) != 1 {
		t.Fatal("the contradiction was refused but no proof was handed out")
	}
}

func TestASenderThatContradictsItsOwnPrepareOrCommitIsProvenToHaveDoneSo(t *testing.T) {
	for _, k := range []Kind{KindPrepare, KindCommit} {
		r := mkReplica(t, 4, "b")
		if _, err := r.Step(signed("c", Message{Kind: k, View: 0, Seq: 1, Digest: Digest{1}})); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		acts, err := r.Step(signed("c", Message{Kind: k, View: 0, Seq: 1, Digest: Digest{2}}))
		if !errors.Is(err, ErrEquivocation) {
			t.Fatalf("%s: got %v, want ErrEquivocation", k, err)
		}
		if countActions[Evidence](acts) != 1 {
			t.Fatalf("%s: the contradiction was refused but no proof was handed out", k)
		}
		for _, a := range acts {
			ev, ok := a.(Evidence)
			if !ok {
				continue
			}
			if err := ev.Proof.Verify(replicaVerifier()); err != nil {
				t.Fatalf("%s: the proof does not hold up for a third party who was not there: %v", k, err)
			}
			if ev.Proof.Offender() != "c" {
				t.Fatalf("%s: the proof incriminates %q", k, ev.Proof.Offender())
			}
		}
	}
}

func TestACheckpointIntervalThatWouldWrapTheWaterMarksIsRefused(t *testing.T) {
	base := []Option{WithSigner(signAs("a")), WithVerifier(replicaVerifier()), WithClientVerifier(clientVerifier())}
	_, err := New(ids(4), "a", append(base, WithCheckpointInterval(1<<62))...)
	if !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("got %v, want ErrInvalidInterval: past a quarter of the sequence space 4K wraps, the high water "+
			"mark lands BELOW the low one, and the replica refuses every sequence number there is while saying "+
			"only that it is outside the water marks", err)
	}
	r, err := New(ids(4), "a", append(base, WithCheckpointInterval(MaxCheckpointInterval))...)
	if err != nil {
		t.Fatalf("the largest supported interval was refused: %v", err)
	}
	if low, high := r.Watermarks(); high <= low {
		t.Fatalf("the water marks at the largest supported interval are (%d, %d]", low, high)
	}
}
