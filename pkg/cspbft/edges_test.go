package cspbft

import (
	"crypto/sha512"
	"errors"
	"fmt"
	"testing"
)

func TestEveryActionTypeReallyImplementsTheActionInterface(t *testing.T) {
	// The AST census in census_test.go says which types claim to be actions;
	// this says the claim compiles and is reachable, so a type that is in the
	// set by name and not by method cannot exist.
	all := []Action{
		Send{}, Broadcast{}, Persist{}, Execute{}, TakeCheckpoint{},
		FetchState{}, SetTimer{}, CancelTimer{}, Evidence{}, Notice{},
	}
	// The interface is unexported and satisfied by an unexported method, so a
	// type that is in the census by name and not by method is a compile error
	// in the line above. What is left to check is that the two lists are the
	// same list: the census parses the source for isAction receivers, and a
	// type nobody constructs here is a type the census counts and the protocol
	// never emits.
	named := map[string]bool{}
	for _, a := range all {
		named[fmt.Sprintf("%T", a)] = true
	}
	for _, want := range []string{
		"cspbft.Send", "cspbft.Broadcast", "cspbft.Persist", "cspbft.Execute",
		"cspbft.TakeCheckpoint", "cspbft.FetchState", "cspbft.SetTimer",
		"cspbft.CancelTimer", "cspbft.Evidence", "cspbft.Notice",
	} {
		if !named[want] {
			t.Fatalf("%s is not in the constructed set", want)
		}
	}
	if len(named) != len(all) {
		t.Fatalf("the action set names %d distinct types across %d values", len(named), len(all))
	}
}

func TestEveryTruncationOfAJournalRecordIsRefused(t *testing.T) {
	pp := signed("a", Message{Kind: KindPrePrepare, View: 1, Seq: 2, Digest: Digest{4}})
	prep := signed("c", Message{Kind: KindPrepare, View: 1, Seq: 2, Digest: Digest{4}})
	cp := signed("b", Message{Kind: KindCheckpoint, Seq: 8, Digest: Digest{5}})
	nested := signed("a", Message{
		Kind: KindViewChange, View: 3, Seq: 8, Digest: Digest{5},
		Checkpoints: []Message{cp},
		Prepared:    []Certificate{{PrePrepare: pp, Prepares: []Message{prep}}},
	})
	full := Record{Kind: RecordMessage, Message: signed("a", Message{
		Kind: KindNewView, View: 3, ViewChanges: []Message{nested}, PrePrepares: []Message{pp},
		Epoch: 8, RotationDigest: Digest{2},
	})}.Encode()

	// Every prefix short of the whole thing must be refused. A decoder that
	// accepts a truncated record is a decoder that silently forgets whatever
	// was in the tail — which, for a journal, is whatever this replica most
	// recently voted for.
	for i := 0; i < len(full); i++ {
		if _, err := DecodeRecord(full[:i]); !errors.Is(err, ErrCorruptRecord) {
			t.Fatalf("a %d-byte prefix of a %d-byte record decoded", i, len(full))
		}
	}
	if _, err := DecodeRecord(full); err != nil {
		t.Fatalf("the whole record did not decode: %v", err)
	}

	// And every single-byte corruption: a record that still decodes must
	// decode to something that is no longer this record. A decoder that
	// accepts damage and hands back a DIFFERENT vote is worse than one that
	// refuses, because the caller has no way to tell.
	checked := 0
	for i := len(recordDomain); i < len(full); i++ {
		damaged := append([]byte(nil), full...)
		damaged[i] ^= 0xff
		got, err := DecodeRecord(damaged)
		if err != nil {
			checked++
			continue
		}
		if string(got.Encode()) == string(full) {
			t.Fatalf("flipping byte %d decoded back to the undamaged record", i)
		}
		checked++
	}
	if checked != len(full)-len(recordDomain) {
		t.Fatalf("%d of %d corruptions were examined", checked, len(full)-len(recordDomain))
	}
}

func TestARestoredJournalPutsBackCheckpointsViewChangesAndNewViews(t *testing.T) {
	r := mkReplica(t, 4, "b")
	vc := signed("c", Message{Kind: KindViewChange, View: 1})
	own := signed("b", Message{Kind: KindViewChange, View: 1})
	nv := signed("b", Message{Kind: KindNewView, View: 1, ViewChanges: []Message{vc, own}})
	records := []Record{
		rec(signed("a", Message{Kind: KindCheckpoint, Seq: 4, Digest: Digest{1}})),
		rec(vc), rec(own), rec(nv),
		rec(clientRequest("c", 1, "one")),
	}
	if _, err := r.Restore(records); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if r.CurrentView() != 1 {
		t.Fatalf("view is %d, want the one the journal says it entered", r.CurrentView())
	}
	if !r.sentViewChange[1] || !r.sentNewView[1] {
		t.Fatal("a restored replica that forgot it already sent a view-change or a new-view would send a second one")
	}
	if len(r.viewChanges[1]) != 2 {
		t.Fatalf("the restored view-change set holds %d", len(r.viewChanges[1]))
	}
	if len(r.requests) != 0 {
		t.Fatalf("%d payloads survived a restore that put back no assignment naming them: a journal is not truncated "+
			"by this package, so keeping every payload it ever held grows a replica's memory with each restart", len(r.requests))
	}
	// A message at or below a restored stable checkpoint is dropped rather
	// than re-admitted below the low water mark.
	r2 := mkReplica(t, 4, "b")
	if _, err := r2.Restore([]Record{
		{Kind: RecordCheckpointStable, Seq: 8, Digest: Digest{1}},
		rec(signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 3, Digest: Digest{2}})),
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(r2.log.entries) != 0 {
		t.Fatal("a record below the restored low water mark was re-admitted")
	}
}

func TestACustomHasherIsUsedForEveryDigestThisPackageComputes(t *testing.T) {
	h := func(b []byte) Digest {
		sum := sha512.Sum512(b)
		var d Digest
		copy(d[:], sum[:])
		return d
	}
	r := mkReplica(t, 4, "b", WithHasher(h))
	m := Message{Kind: KindRequest, Client: "c", Serial: 1, Proposal: Proposal("one")}
	m.Digest = RequestDigest(h, "c", 1, m.Proposal)
	m.Signature = sigFor("client", "c", Preimage(m))
	if _, err := r.Step(m); err != nil {
		t.Fatalf("a request digested with the configured hasher was refused: %v", err)
	}
	sha := mkReplica(t, 4, "b")
	if _, err := sha.Step(m); !errors.Is(err, ErrMalformed) {
		t.Fatal("a replica configured with a different hasher accepted it, so the hasher is not actually used")
	}
}

func TestTheMemoryOfWhatWasExecutedEndsAtTheStableCheckpointAndNowhereElse(t *testing.T) {
	// It has to end at a point every replica agrees on. A replica that took
	// the state at a checkpoint by transfer never executed anything below it,
	// so a memory reaching further back would be held by the replicas that
	// were there and not by the one that was not.
	r := mkReplica(t, 4, "b")
	r.executedAt[Digest{1}] = 3
	r.executedAt[Digest{2}] = 7
	r.lastExecuted, r.lastStable = 7, 0
	r.gcRequests(4)
	if _, still := r.executedAt[Digest{1}]; still {
		t.Fatal("a request executed below the stable checkpoint is still remembered")
	}
	if _, gone := r.executedAt[Digest{2}]; !gone {
		t.Fatal("a request executed above the stable checkpoint was forgotten")
	}
}

func TestTheSetOfHeldAssignmentsIsBoundedInMessagesAndNotOnlyInDigests(t *testing.T) {
	// The bound that used to be here counted map keys, which is the one thing
	// a faulty primary does not have to grow: it can retransmit ONE assignment
	// forever, and the slice under that key is what fills.
	r := mkReplica(t, 4, "b") // K=4, so at most 16 assignments may be held
	one := Digest{1}
	for i := 0; i < 5000; i++ {
		r.hold(Message{Kind: KindPrePrepare, From: "a", View: 0, Seq: 1, Digest: one})
	}
	if len(r.orphans[one]) != 1 {
		t.Fatalf("%d copies of one assignment are held: a retransmission is not a second assignment", len(r.orphans[one]))
	}
	for i := 1; i <= 400; i++ {
		r.hold(Message{Kind: KindPrePrepare, From: "a", View: 0, Seq: Sequence(i), Digest: Digest{byte(i), byte(i >> 8)}})
	}
	if r.orphansHeld > 16 {
		t.Fatalf("%d assignments are held across %d digests, want at most 16", r.orphansHeld, len(r.orphans))
	}
	total := 0
	for _, held := range r.orphans {
		total += len(held)
	}
	if total != r.orphansHeld {
		t.Fatalf("the count says %d and the map holds %d: a delete that forgets the count leaks the bound, not the memory", r.orphansHeld, total)
	}
}

func TestATieBetweenTwoCertificatesOfOneViewIsBrokenDeterministically(t *testing.T) {
	// It can only happen if a primary equivocated AND both halves reached a
	// quorum, which the protocol makes impossible — but "impossible" is not a
	// reason for two replicas to compute different O sets.
	if !bytesLess(Digest{1}, Digest{2}) {
		t.Fatal("the tie-break is not an ordering")
	}
	if bytesLess(Digest{2}, Digest{1}) || bytesLess(Digest{5}, Digest{5}) {
		t.Fatal("the tie-break is not antisymmetric")
	}
}

func TestACheckpointProofThatDoesNotHoldUpIsRefused(t *testing.T) {
	good := func(id ReplicaID, seq Sequence, d Digest) Message {
		return signed(id, Message{Kind: KindCheckpoint, Seq: seq, Digest: d})
	}
	forged := good("a", 8, Digest{1})
	forged.Signature = []byte("nope")
	known := func(id ReplicaID) bool { return contains(ids(4), id) }

	for _, tc := range []struct {
		name  string
		proof []Message
		seq   Sequence
	}{
		{"a proof for sequence zero", []Message{good("a", 0, Digest{1})}, 0},
		{"short of a quorum", []Message{good("a", 8, Digest{1}), good("b", 8, Digest{1})}, 8},
		{"certifying two states", []Message{good("a", 8, Digest{1}), good("b", 8, Digest{2}), good("c", 8, Digest{1})}, 8},
		{"for another sequence", []Message{good("a", 8, Digest{1}), good("b", 9, Digest{1}), good("c", 8, Digest{1})}, 8},
		{"holding a prepare", []Message{good("a", 8, Digest{1}), signed("b", Message{Kind: KindPrepare, View: 0, Seq: 8}), good("c", 8, Digest{1})}, 8},
		{"counting one replica twice", []Message{good("a", 8, Digest{1}), good("a", 8, Digest{1}), good("c", 8, Digest{1})}, 8},
		{"holding a stranger", []Message{good("a", 8, Digest{1}), good("zz", 8, Digest{1}), good("c", 8, Digest{1})}, 8},
		{"not verifying", []Message{forged, good("b", 8, Digest{1}), good("c", 8, Digest{1})}, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateCheckpointProof(tc.proof, tc.seq, 3, replicaVerifier(), known); !errors.Is(err, ErrBadCertificate) {
				t.Fatalf("got %v, want ErrBadCertificate", err)
			}
		})
	}
	d, err := validateCheckpointProof([]Message{good("a", 8, Digest{4}), good("b", 8, Digest{4}), good("c", 8, Digest{4})}, 8, 3, replicaVerifier(), known)
	if err != nil || d != (Digest{4}) {
		t.Fatalf("a genuine proof was refused: %v %v", d, err)
	}
	if d, err := validateCheckpointProof(nil, 0, 3, replicaVerifier(), known); err != nil || !d.IsNull() {
		t.Fatalf("before the first checkpoint there is nothing to prove: %v %v", d, err)
	}
}

func TestAViewChangeToAViewThisReplicaIsAlreadyInIsStale(t *testing.T) {
	r := mkReplica(t, 4, "b")
	if _, err := r.Step(viewChangeFrom("c", 0)); err == nil {
		t.Fatal("a view-change to view zero must not be accepted")
	}
	r.view = 5
	if _, err := r.Step(viewChangeFrom("c", 3)); !errors.Is(err, ErrStaleView) {
		t.Fatalf("want ErrStaleView")
	}
}

func TestANewViewThatIsAlreadyInstalledIsADuplicateAndNotAnError(t *testing.T) {
	primary := mkReplica(t, 4, "b")
	nv := newViewBuiltBy(t, primary, []ReplicaID{"c", "d"}, 1, certFor(0, 1, Digest{7}, "a", "c", "d"))
	backup := mkReplica(t, 4, "c")
	mustStep(t, backup, nv)
	if _, err := backup.Step(nv); !errors.Is(err, ErrDuplicateVote) {
		t.Fatalf("got %v, want ErrDuplicateVote", err)
	}
	backup.view = 9
	if _, err := backup.Step(nv); !errors.Is(err, ErrStaleView) {
		t.Fatalf("got %v, want ErrStaleView", err)
	}
}

func TestAPrimaryStopsAssigningWhileItIsChangingView(t *testing.T) {
	r := mkReplica(t, 4, "a") // the primary of view 0
	req := clientRequest("c", 1, "one")
	acts := mustStep(t, r, req)
	if _, ok := findBroadcast(acts, KindPrePrepare); !ok {
		t.Fatal("the primary must assign a sequence number to a request it holds")
	}
	r.Timeout(TimerRequest)
	acts = mustStep(t, r, clientRequest("c", 2, "two"))
	if _, ok := findBroadcast(acts, KindPrePrepare); ok {
		t.Fatal("a primary that has already asked to be replaced must not keep assigning in the view it is leaving")
	}
}

func TestAPrimaryStopsAssigningAtTheHighWaterMark(t *testing.T) {
	r := mkReplica(t, 4, "a") // K=4, so the window is (0, 8]
	for i := uint64(1); i <= 12; i++ {
		mustStep(t, r, clientRequest("c", i, "p"))
	}
	if r.lastAssigned != 8 {
		t.Fatalf("the primary assigned up to %d, want to stop at the high water mark of 8: the window is what stops "+
			"a primary running ahead of a cluster that cannot checkpoint", r.lastAssigned)
	}
}
