package cspbft

import (
	"errors"
	"testing"
	"time"
)

// certFor builds a prepared certificate the way a correct replica would hold
// one: the primary's assignment and q-1 prepares from distinct others.
func certFor(view View, seq Sequence, d Digest, primary ReplicaID, preparers ...ReplicaID) Certificate {
	c := Certificate{PrePrepare: signed(primary, Message{Kind: KindPrePrepare, View: view, Seq: seq, Digest: d})}
	for _, p := range preparers {
		c.Prepares = append(c.Prepares, signed(p, Message{Kind: KindPrepare, View: view, Seq: seq, Digest: d}))
	}
	return c
}

func viewChangeFrom(from ReplicaID, to View, certs ...Certificate) Message {
	return signed(from, Message{Kind: KindViewChange, View: to, Prepared: certs})
}

// newViewBuiltBy drives b through a view change until it emits the NEW-VIEW it
// is responsible for, and returns it.
func newViewBuiltBy(t *testing.T, r *Replica, from []ReplicaID, to View, certs ...Certificate) Message {
	t.Helper()
	r.Timeout(TimerRequest)
	var nv Message
	for i, id := range from {
		var acts []Action
		var err error
		if i == 0 {
			acts, err = r.Step(viewChangeFrom(id, to, certs...))
		} else {
			acts, err = r.Step(viewChangeFrom(id, to))
		}
		if err != nil {
			t.Fatalf("view-change from %s: %v", id, err)
		}
		if m, ok := findBroadcast(acts, KindNewView); ok {
			nv = m
		}
	}
	if nv.Kind != KindNewView {
		t.Fatal("the primary of the new view never built a NEW-VIEW")
	}
	return nv
}

func TestAReplicaSendsAtMostOneViewChangePerView(t *testing.T) {
	r := mkReplica(t, 4, "c")
	first := r.Timeout(TimerRequest)
	if _, ok := findBroadcast(first, KindViewChange); !ok {
		t.Fatal("a request timeout must start a view change")
	}
	second := r.Timeout(TimerRequest)
	for _, a := range second {
		if b, ok := a.(Broadcast); ok && b.Message.Kind == KindViewChange && b.Message.View == 1 {
			t.Fatal("a second view-change for one view: one replica would then supply two of the votes a NEW-VIEW needs")
		}
	}
}

func TestANewViewReproposesEveryDigestPreparedInAnEarlierView(t *testing.T) {
	r := mkReplica(t, 4, "b") // primary of view 1
	d := Digest{7}
	nv := newViewBuiltBy(t, r, []ReplicaID{"c", "d"}, 1, certFor(0, 1, d, "a", "c", "d"))
	if len(nv.PrePrepares) != 1 {
		t.Fatalf("O is %v, want one re-proposal", nv.PrePrepares)
	}
	if nv.PrePrepares[0].Seq != 1 || nv.PrePrepares[0].Digest != d {
		t.Fatalf("O re-proposes %+v, want sequence 1 with the prepared digest: a quorum was already promised this assignment", nv.PrePrepares[0])
	}
	if nv.Epoch != r.Epoch() || nv.RotationDigest != r.rot.digest {
		t.Fatal("a NEW-VIEW must name the rotation it was built under")
	}
}

func TestANewViewFillsGapsWithNullRequests(t *testing.T) {
	r := mkReplica(t, 4, "b")
	d := Digest{7}
	nv := newViewBuiltBy(t, r, []ReplicaID{"c", "d"}, 1, certFor(0, 3, d, "a", "c", "d"))
	if len(nv.PrePrepares) != 3 {
		t.Fatalf("O is %v, want sequences 1..3", nv.PrePrepares)
	}
	for i, pp := range nv.PrePrepares {
		wantNull := i < 2
		if pp.Digest.IsNull() != wantNull {
			t.Fatalf("O[%d] at sequence %d: null=%v, want %v — a hole in the order stops execution forever",
				i, pp.Seq, pp.Digest.IsNull(), wantNull)
		}
	}
}

func TestTheHighestViewCertificateWinsWhenTwoViewsPreparedOneSequence(t *testing.T) {
	r := mkReplica(t, 4, "d") // primary of view 3
	older := certFor(0, 1, Digest{1}, "a", "b", "c")
	newer := certFor(1, 1, Digest{2}, "b", "a", "c")
	r.Timeout(TimerRequest)
	r.view = 2
	r.pendingView = 0
	r.viewChanging = false
	r.Timeout(TimerRequest)
	var nv Message
	for _, vc := range []Message{
		signed("a", Message{Kind: KindViewChange, View: 3, Prepared: []Certificate{older}}),
		signed("b", Message{Kind: KindViewChange, View: 3, Prepared: []Certificate{newer}}),
	} {
		acts, err := r.Step(vc)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if m, ok := findBroadcast(acts, KindNewView); ok {
			nv = m
		}
	}
	if nv.Kind != KindNewView || len(nv.PrePrepares) != 1 {
		t.Fatalf("no NEW-VIEW with one assignment: %+v", nv)
	}
	if nv.PrePrepares[0].Digest != (Digest{2}) {
		t.Fatal("the older view's digest won: cross-view stickiness is the whole reason the commit phase exists")
	}
}

func TestABackupIndependentlyRecomputesTheOSetAndRefusesOneThatDiffers(t *testing.T) {
	primary := mkReplica(t, 4, "b")
	d := Digest{7}
	nv := newViewBuiltBy(t, primary, []ReplicaID{"c", "d"}, 1, certFor(0, 1, d, "a", "c", "d"))

	backup := mkReplica(t, 4, "c")
	if _, err := backup.Step(nv); err != nil {
		t.Fatalf("a backup refused a NEW-VIEW it should have recomputed identically: %v", err)
	}
	if backup.CurrentView() != 1 {
		t.Fatalf("the backup is in view %d", backup.CurrentView())
	}

	tampered := nv
	tampered.PrePrepares = append([]Message(nil), nv.PrePrepares...)
	tampered.PrePrepares[0] = signed("b", Message{Kind: KindPrePrepare, View: 1, Seq: 1, Digest: Digest{99}})
	tampered = signed("b", tampered)
	other := mkReplica(t, 4, "d")
	if _, err := other.Step(tampered); !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("got %v, want ErrBadCertificate: a backup that accepts an O set which dropped a prepared "+
			"certificate has agreed to forget an assignment a quorum already made", err)
	}
}

func TestANewViewThatIsShortOfAQuorumOrMisattributedIsRefused(t *testing.T) {
	primary := mkReplica(t, 4, "b")
	nv := newViewBuiltBy(t, primary, []ReplicaID{"c", "d"}, 1, certFor(0, 1, Digest{7}, "a", "c", "d"))

	type testCase struct {
		name string
		make func(Message) Message
		want error
	}
	for _, tc := range []testCase{
		{"short of a quorum", func(m Message) Message {
			m.ViewChanges = m.ViewChanges[:1]
			return signed("b", m)
		}, ErrBadCertificate},
		{"one sender counted twice", func(m Message) Message {
			m.ViewChanges = []Message{m.ViewChanges[0], m.ViewChanges[0], m.ViewChanges[0]}
			return signed("b", m)
		}, ErrBadCertificate},
		{"a view-change for another view", func(m Message) Message {
			vcs := append([]Message(nil), m.ViewChanges...)
			vcs[0] = signed("c", Message{Kind: KindViewChange, View: 9})
			m.ViewChanges = vcs
			return signed("b", m)
		}, ErrBadCertificate},
		{"a view-change that does not verify", func(m Message) Message {
			vcs := append([]Message(nil), m.ViewChanges...)
			vcs[0].Signature = []byte("forged")
			m.ViewChanges = vcs
			return signed("b", m)
		}, ErrBadCertificate},
		{"from someone who is not the primary", func(m Message) Message {
			return signed("d", m)
		}, ErrNotPrimary},
		{"an assignment that does not verify", func(m Message) Message {
			pps := append([]Message(nil), m.PrePrepares...)
			pps[0].Signature = []byte("forged")
			m.PrePrepares = pps
			return signed("b", m)
		}, ErrBadCertificate},
		{"an assignment attributed to someone else", func(m Message) Message {
			pps := append([]Message(nil), m.PrePrepares...)
			pps[0] = signed("c", Message{Kind: KindPrePrepare, View: 1, Seq: 1, Digest: pps[0].Digest})
			m.PrePrepares = pps
			return signed("b", m)
		}, ErrBadCertificate},
		{"an O set of the wrong length", func(m Message) Message {
			m.PrePrepares = nil
			return signed("b", m)
		}, ErrBadCertificate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mkReplica(t, 4, "c")
			if _, err := r.Step(tc.make(nv)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAViewChangeWhoseCertificatesDoNotHoldUpIsRefused(t *testing.T) {
	good := certFor(0, 1, Digest{7}, "a", "c", "d")
	type testCase struct {
		name string
		vc   Message
	}
	for _, tc := range []testCase{
		{"a certificate short of a quorum", viewChangeFrom("c", 1, certFor(0, 1, Digest{7}, "a", "c"))},
		{"a certificate counting one replica twice", viewChangeFrom("c", 1, certFor(0, 1, Digest{7}, "a", "c", "c"))},
		{"a certificate counting the primary's own prepare", viewChangeFrom("c", 1, certFor(0, 1, Digest{7}, "a", "a", "c"))},
		{"a certificate holding a prepare for another assignment", viewChangeFrom("c", 1, Certificate{
			PrePrepare: good.PrePrepare,
			Prepares:   []Message{good.Prepares[0], signed("d", Message{Kind: KindPrepare, View: 0, Seq: 2, Digest: Digest{7}})},
		})},
		{"a certificate from a replica outside the configuration", viewChangeFrom("c", 1, certFor(0, 1, Digest{7}, "a", "c", "zz"))},
		{"two certificates for one sequence", viewChangeFrom("c", 1, good, certFor(0, 1, Digest{8}, "a", "c", "d"))},
		{"a certificate from the view being moved to", viewChangeFrom("c", 1, certFor(1, 1, Digest{7}, "b", "c", "d"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mkReplica(t, 4, "b")
			if _, err := r.Step(tc.vc); !errors.Is(err, ErrBadCertificate) {
				t.Fatalf("got %v, want ErrBadCertificate", err)
			}
		})
	}
}

func TestAViewChangeWhoseCertificateDoesNotVerifyIsRefused(t *testing.T) {
	c := certFor(0, 1, Digest{7}, "a", "c", "d")
	c.PrePrepare.Signature = []byte("forged")
	r := mkReplica(t, 4, "b")
	if _, err := r.Step(viewChangeFrom("c", 1, c)); !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("got %v, want ErrBadCertificate", err)
	}
	c2 := certFor(0, 1, Digest{7}, "a", "c", "d")
	c2.Prepares[0].Signature = []byte("forged")
	if _, err := r.Step(viewChangeFrom("d", 1, c2)); !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("got %v, want ErrBadCertificate for a forged prepare", err)
	}
}

func TestAReplicaJumpsForwardOnFPlusOneViewChangesForHigherViews(t *testing.T) {
	r := mkReplica(t, 4, "a") // f = 1, so two view-changes above view 0 pull it forward
	acts, err := r.Step(viewChangeFrom("c", 5))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findBroadcast(acts, KindViewChange); ok {
		t.Fatal("one view-change moved this replica: f+1 is what makes the claim true of somebody correct")
	}
	acts, err = r.Step(viewChangeFrom("d", 7))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := findBroadcast(acts, KindViewChange)
	if !ok {
		t.Fatal("f+1 view-changes for higher views must pull a lagging replica forward without waiting for its own timer")
	}
	if m.View != 5 {
		t.Fatalf("it moved to view %d, want the smallest of the set", m.View)
	}
}

func TestTheViewTimeoutDoublesAndIsCapped(t *testing.T) {
	r := mkReplica(t, 4, "c", WithBaseTimeout(time.Second), WithMaxTimeout(4*time.Second))
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second}
	for i, w := range want {
		var acts []Action
		if i == 0 {
			acts = r.Timeout(TimerRequest)
		} else {
			acts = r.Timeout(TimerNewView)
		}
		var got time.Duration
		for _, a := range acts {
			if st, ok := a.(SetTimer); ok && st.Which == TimerNewView {
				got = st.After
			}
		}
		if got != w {
			t.Fatalf("round %d waited %s, want %s — without the doubling a partition burns through views faster "+
				"than a NEW-VIEW can cross it, and without the cap the cluster takes hours to notice it healed", i, got, w)
		}
	}
}

func TestTheViewTimeoutResetsAfterExecutingInTheNewView(t *testing.T) {
	// Seven replicas, so two unreachable ones are inside the fault budget and
	// a quorum of five is still available.
	s := newSim(t, 7, 11, withSimInterval(8))
	base := 4 * time.Second
	// The first two primaries are unreachable, so the first view change fails
	// and the timeout doubles before the third primary makes progress.
	s.down["r00"] = true
	s.down["r01"] = true
	s.request("c1", 1, "alpha")
	s.drain()
	s.tick() // request timers fire: view change to 1, whose primary is also down
	s.drain()
	if got := s.rep["r02"].timeout; got != base {
		t.Fatalf("the first view change already doubled: %s", got)
	}
	s.tick() // the new-view timer fires: doubling, and a view change to 2
	if got := s.rep["r02"].timeout; got != 2*base {
		t.Fatalf("timeout is %s, want %s after a failed view change", got, 2*base)
	}
	s.settle(10)
	s.requireExecuted(1, "alpha", "r02", "r03")
	if got := s.rep["r02"].timeout; got != base {
		t.Fatalf("timeout is %s after executing in the new view, want the base %s: executing means the view works", got, base)
	}
}

func TestAReplicaFarBehindWhenANewViewArrivesAdoptsItsCheckpointAndAsksForTheState(t *testing.T) {
	state := Digest{0xaa}
	var proof []Message
	for _, id := range []ReplicaID{"a", "c", "d"} {
		proof = append(proof, signed(id, Message{Kind: KindCheckpoint, Seq: 8, Digest: state}))
	}
	ahead := func(from ReplicaID) Message {
		return signed(from, Message{
			Kind: KindViewChange, View: 1, Seq: 8, Digest: state,
			Checkpoints: proof,
			Prepared:    []Certificate{certFor(0, 9, Digest{7}, "a", "c", "d")},
		})
	}

	primary := mkReplica(t, 4, "b")
	primary.Timeout(TimerRequest)
	var nv Message
	for _, vc := range []Message{ahead("c"), ahead("d")} {
		acts, err := primary.Step(vc)
		if err != nil {
			t.Fatalf("view-change: %v", err)
		}
		if m, ok := findBroadcast(acts, KindNewView); ok {
			nv = m
		}
		for _, a := range acts {
			if f, ok := a.(FetchState); ok {
				if f.Seq != 8 || f.Digest != state || len(f.From) != 3 {
					t.Fatalf("FetchState is %+v, want the sequence, the digest and the replicas that certified it", f)
				}
			}
		}
	}
	if nv.Kind != KindNewView {
		t.Fatal("no NEW-VIEW")
	}
	if primary.lastStable != 8 {
		t.Fatalf("the new primary's low water mark is %d: a new view whose floor is above this replica's must move it, "+
			"because the order below that floor has been discarded everywhere", primary.lastStable)
	}

	backup := mkReplica(t, 4, "c")
	acts := mustStep(t, backup, nv)
	if countActions[FetchState](acts) != 1 {
		t.Fatalf("a backup that was behind was not told to fetch the state: %v", acts)
	}
	if backup.lastStable != 8 {
		t.Fatalf("the backup's low water mark is %d, want 8", backup.lastStable)
	}
	if backup.CurrentView() != 1 {
		t.Fatalf("the backup is in view %d", backup.CurrentView())
	}
	if _, err := backup.Installed(8, state); err != nil {
		t.Fatalf("installing the certified state: %v", err)
	}
	if backup.LastExecuted() != 8 {
		t.Fatalf("LastExecuted is %d after the transfer", backup.LastExecuted())
	}
}

func TestAReplicaThatEscalatedPastAViewItThenEntersKeepsTimingOut(t *testing.T) {
	r := mkReplica(t, 4, "d")
	if _, ok := findBroadcast(r.Timeout(TimerRequest), KindViewChange); !ok {
		t.Fatal("a request timeout must start a view change")
	}
	second, ok := findBroadcast(r.Timeout(TimerNewView), KindViewChange)
	if !ok || second.View != 2 {
		t.Fatalf("the escalation asked for view %d, want 2", second.View)
	}

	// The NEW-VIEW for view 1 finally lands: the replica enters a view it has
	// already escalated past.
	nv := newViewBuiltBy(t, mkReplica(t, 4, "b"), []ReplicaID{"a", "c"}, 1)
	if _, err := r.Step(nv); err != nil {
		t.Fatalf("new-view: %v", err)
	}
	if r.CurrentView() != 1 {
		t.Fatalf("the replica is in view %d", r.CurrentView())
	}
	req := clientRequest("c", 1, "one")
	if countActions[SetTimer](mustStep(t, r, req)) == 0 {
		t.Fatal("the request timer was not armed for an unexecuted request")
	}

	out := r.Timeout(TimerRequest)
	vc, ok := findBroadcast(out, KindViewChange)
	if !ok {
		t.Fatalf("the timeout produced %d actions and no view-change.\n\n"+
			"Timeout has already marked the timer stopped by the time it asks for one, and only an inbound "+
			"assignment from the very primary it is timing out on re-arms it — so a replica that declines here "+
			"is left holding an unexecuted request with no fault detector at all, and every further timeout "+
			"does the same.", len(out))
	}
	if vc.View != 3 {
		t.Fatalf("it asked for view %d: views 1 and 2 are already spoken for", vc.View)
	}
}

func TestAViewChangeNamingAStateItsOwnProofDoesNotCertifyIsRefused(t *testing.T) {
	known := func(id ReplicaID) bool { return contains(ids(4), id) }
	q := Quorum(4)
	var proof []Message
	for _, id := range ids(4)[:q] {
		proof = append(proof, signed(id, Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{0xaa}}))
	}
	honest := signed("a", Message{Kind: KindViewChange, View: 1, Seq: 4, Digest: Digest{0xaa}, Checkpoints: proof})
	if err := validateViewChange(honest, q, replicaVerifier(), known); err != nil {
		t.Fatalf("a view-change naming the state its own proof certifies was refused: %v", err)
	}
	lie := signed("a", Message{Kind: KindViewChange, View: 1, Seq: 4, Digest: Digest{0xbb}, Checkpoints: proof})
	err := validateViewChange(lie, q, replicaVerifier(), known)
	if !errors.Is(err, ErrBadCertificate) {
		t.Fatalf("got %v, want ErrBadCertificate.\n\n"+
			"This is the one field a sender asserts that is USED rather than carried: a new view adopts it as the "+
			"low water mark and asks the caller to fetch the bytes behind it. Unchecked, one faulty replica makes "+
			"every replica that is behind adopt a checkpoint nobody holds, discard its log through it, and then "+
			"refuse the real state as disagreeing with it — and correct replicas relay the lie afterwards, with a "+
			"proof that verifies.", err)
	}
}

func TestOneReplicaCannotMakeEveryOtherRememberAndJournalUnboundedlyManyViews(t *testing.T) {
	r := mkReplica(t, 4, "b")
	accepted, records := 0, 0
	for v := View(1); v <= 5000; v++ {
		acts, err := r.Step(signed("a", Message{Kind: KindViewChange, View: v}))
		if err != nil {
			if !errors.Is(err, ErrTooManyViewChanges) {
				t.Fatalf("view %d: %v", v, err)
			}
			continue
		}
		accepted++
		for _, a := range acts {
			if p, ok := a.(Persist); ok {
				records += len(p.Records)
			}
		}
	}
	if want := 2*Faults(4) + 2; accepted > want {
		t.Fatalf("%d of one replica's view-changes were accepted and %d journal records demanded, want at most %d.\n\n"+
			"A view is not a sequence number, so nothing else bounds how far ahead one may name — and every "+
			"accepted one is a durable write on every correct replica, at one cheap packet each.", accepted, records, want)
	}
	// The bound is per sender, so no replica can use it to crowd out another.
	if _, err := r.Step(signed("c", Message{Kind: KindViewChange, View: 1})); err != nil {
		t.Fatalf("another replica's first view-change was refused after the flood: %v", err)
	}
}

func TestEnteringAViewAsksAgainWhetherThisReplicaIsAlreadyBehindTheNextOne(t *testing.T) {
	r := mkReplica(t, 4, "d")
	for _, id := range []ReplicaID{"a", "b", "c"} {
		if _, err := r.Step(viewChangeFrom(id, 1)); err != nil {
			t.Fatalf("view-change for 1 from %s: %v", id, err)
		}
	}
	for _, id := range []ReplicaID{"a", "b"} {
		if _, err := r.Step(viewChangeFrom(id, 3)); err != nil {
			t.Fatalf("view-change for 3 from %s: %v", id, err)
		}
	}
	nv := newViewBuiltBy(t, mkReplica(t, 4, "b"), []ReplicaID{"a", "c"}, 1)
	acts, err := r.Step(nv)
	if err != nil {
		t.Fatalf("new-view: %v", err)
	}
	if r.CurrentView() == 0 {
		t.Fatal("the new-view was not installed")
	}
	vc, ok := findBroadcast(acts, KindViewChange)
	if !ok || vc.View != 3 {
		t.Fatal("entering view 1 produced no view-change for view 3.\n\n" +
			"hearsay and maybeSendNewView are otherwise reachable only from an inbound VIEW-CHANGE, so a quorum " +
			"that arrived while this replica was in a lower view sits in the map unread — and the replica stays " +
			"in a view the rest of the cluster has already left, with nothing left to notice it.")
	}
}

func TestAReplicaDoesNotCountItsOwnAskTowardTheFPlusOneThatMovesIt(t *testing.T) {
	// The rule is f+1 OTHERS. Counting this replica's own ask weakens it to f,
	// which f faulty replicas reach by themselves.
	r := mkReplica(t, 4, "d")
	r.Timeout(TimerRequest) // d now holds its own view-change for view 1
	acts, err := r.Step(viewChangeFrom("a", 2))
	if err != nil {
		t.Fatalf("view-change: %v", err)
	}
	for _, a := range acts {
		if b, ok := a.(Broadcast); ok && b.Message.Kind == KindViewChange && b.Message.View == 2 {
			t.Fatal("one other replica's ask plus this replica's own moved it: f faulty replicas would then " +
				"move every correct replica whenever any of them had timed out")
		}
	}
	moved, err := r.Step(viewChangeFrom("b", 2))
	if err != nil {
		t.Fatalf("view-change: %v", err)
	}
	if vc, ok := findBroadcast(moved, KindViewChange); !ok || vc.View != 2 {
		t.Fatal("two other replicas asking for view 2 did not move it")
	}
}

func TestTheMemoryOfWhichViewsWereAskedForDoesNotGrowForever(t *testing.T) {
	r := mkReplica(t, 4, "d")
	r.Timeout(TimerRequest) // view 1
	r.Timeout(TimerNewView) // view 2
	if !r.sentViewChange[1] || !r.sentViewChange[2] {
		t.Fatal("the replica did not ask for the two views it timed out through")
	}

	// The primary of view 2 escalates the same way and then holds a quorum.
	primary := mkReplica(t, 4, "c")
	primary.Timeout(TimerRequest)
	primary.Timeout(TimerNewView)
	var nv Message
	for _, id := range []ReplicaID{"a", "b"} {
		acts, err := primary.Step(viewChangeFrom(id, 2))
		if err != nil {
			t.Fatalf("view-change from %s: %v", id, err)
		}
		if m, ok := findBroadcast(acts, KindNewView); ok {
			nv = m
		}
	}
	if nv.Kind != KindNewView {
		t.Fatal("the primary of view 2 never built a NEW-VIEW")
	}
	if _, err := r.Step(nv); err != nil {
		t.Fatalf("new-view: %v", err)
	}
	if r.sentViewChange[1] || len(r.sentViewChange) != 1 {
		t.Fatalf("%d remembered asks survived entering view %d.\n\n"+
			"startViewChange refuses a view at or below the current one anyway, so a yes about an older view "+
			"answers a question nobody asks again — and this package bounds the log, the checkpoints, the held "+
			"assignments and the view-change set, so a map that grows one entry per view for the life of the "+
			"process would be the exception with no reason behind it.", len(r.sentViewChange), r.CurrentView())
	}
}

func TestAnUnreachableEntryFromAnOlderViewDoesNotSurviveTheViewChange(t *testing.T) {
	// A message for a view below the current one is refused at the door, so an
	// entry that is not prepared when the view moves can never become prepared
	// and can appear in no certificate.
	r := mkReplica(t, 4, "d")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))
	if len(r.log.entries) != 1 {
		t.Fatalf("the log holds %d entries before the view change", len(r.log.entries))
	}
	nv := newViewBuiltBy(t, mkReplica(t, 4, "b"), []ReplicaID{"a", "c"}, 1)
	if _, err := r.Step(nv); err != nil {
		t.Fatalf("new-view: %v", err)
	}
	for k := range r.log.entries {
		if k.view == 0 {
			t.Fatalf("an unprepared entry for view 0 survived into view %d: nothing can ever be added to it, "+
				"so a long run of view changes with no checkpoint in between grows the log without bound",
				r.CurrentView())
		}
	}
}

func TestAReplicaStopsAskingForViewsPastTheWindowTheAnswerMustBeIn(t *testing.T) {
	r := mkReplica(t, 4, "d")
	budget := 2*Faults(4) + 2
	var last View
	for i := 0; i < budget; i++ {
		vc, ok := findBroadcast(r.Timeout(TimerRequest), KindViewChange)
		if !ok {
			t.Fatalf("the replica stopped asking after %d views", i)
		}
		last = vc.View
	}
	if int(last) != budget {
		t.Fatalf("it reached view %d after %d timeouts", last, budget)
	}

	out := r.Timeout(TimerRequest)
	if vc, ok := findBroadcast(out, KindViewChange); ok {
		t.Fatalf("it asked for view %d.\n\n"+
			"A run of faulty primaries is at most 2f long, so one of the views it is already asking for has a "+
			"correct primary: the answer is in that window or it is not coming, and every view past it is one "+
			"more signed message every correct replica has to remember and write to disk.", vc.View)
	}
	if countActions[SetTimer](out) != 1 {
		t.Fatal("it stopped asking AND stopped waiting, which is how a replica loses its only fault detector")
	}
	if !r.roomForViewChange(1, "d") {
		t.Fatal("a view this replica is already remembered for was counted as a new ask")
	}
}
