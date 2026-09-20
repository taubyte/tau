package cspbft

import (
	"errors"
	"testing"
)

func TestAMessageCarryingFieldsThatDoNotBelongToItsKindIsMalformed(t *testing.T) {
	sig := []byte("s")
	for _, tc := range []struct {
		name string
		m    Message
	}{
		{"no signature", Message{Kind: KindPrepare, From: "a", Seq: 1}},
		{"unknown kind", Message{Kind: Kind(42), Signature: sig}},
		{"a request naming a replica", Message{Kind: KindRequest, From: "a", Client: "c", Signature: sig}},
		{"a request with no client", Message{Kind: KindRequest, Signature: sig}},
		{"a request carrying a view", Message{Kind: KindRequest, Client: "c", View: 1, Signature: sig}},
		{"a request carrying a certificate", Message{Kind: KindRequest, Client: "c", Prepared: []Certificate{{}}, Signature: sig}},
		{"a prepare with no sender", Message{Kind: KindPrepare, Seq: 1, Signature: sig}},
		{"a prepare naming a client", Message{Kind: KindPrepare, From: "a", Client: "c", Seq: 1, Signature: sig}},
		{"a prepare at sequence zero", Message{Kind: KindPrepare, From: "a", Signature: sig}},
		{"a commit carrying a proposal", Message{Kind: KindCommit, From: "a", Seq: 1, Proposal: Proposal("x"), Signature: sig}},
		{"a commit carrying view-changes", Message{Kind: KindCommit, From: "a", Seq: 1, ViewChanges: []Message{{}}, Signature: sig}},
		{"a checkpoint carrying a view", Message{Kind: KindCheckpoint, From: "a", View: 2, Seq: 4, Signature: sig}},
		{"a checkpoint carrying a rotation", Message{Kind: KindCheckpoint, From: "a", Seq: 4, Epoch: 1, Signature: sig}},
		{"a view-change for view zero", Message{Kind: KindViewChange, From: "a", Signature: sig}},
		{"a view-change carrying an O set", Message{Kind: KindViewChange, From: "a", View: 1, PrePrepares: []Message{{}}, Signature: sig}},
		{"a view-change whose C holds a prepare", Message{Kind: KindViewChange, From: "a", View: 1,
			Checkpoints: []Message{{Kind: KindPrepare, From: "b", Seq: 1, Signature: sig}}, Signature: sig}},
		{"a view-change whose certificate is not a pre-prepare", Message{Kind: KindViewChange, From: "a", View: 1,
			Prepared: []Certificate{{PrePrepare: Message{Kind: KindPrepare, From: "b", Seq: 1, Signature: sig}}}, Signature: sig}},
		{"a certificate holding a commit among its prepares", Message{Kind: KindViewChange, From: "a", View: 1,
			Prepared: []Certificate{{
				PrePrepare: Message{Kind: KindPrePrepare, From: "b", Seq: 1, Signature: sig},
				Prepares:   []Message{{Kind: KindCommit, From: "c", Seq: 1, Signature: sig}},
			}}, Signature: sig}},
		{"a new-view for view zero", Message{Kind: KindNewView, From: "a", Signature: sig}},
		{"a new-view carrying a checkpoint proof", Message{Kind: KindNewView, From: "a", View: 1,
			Checkpoints: []Message{{}}, Signature: sig}},
		{"a new-view whose V holds a commit", Message{Kind: KindNewView, From: "a", View: 1,
			ViewChanges: []Message{{Kind: KindCommit, From: "b", Seq: 1, Signature: sig}}, Signature: sig}},
		{"a new-view whose O holds a prepare", Message{Kind: KindNewView, From: "a", View: 1,
			PrePrepares: []Message{{Kind: KindPrepare, From: "b", Seq: 1, Signature: sig}}, Signature: sig}},
		{"a new-view whose O does not validate", Message{Kind: KindNewView, From: "a", View: 1,
			PrePrepares: []Message{{Kind: KindPrePrepare, From: "b", Signature: sig}}, Signature: sig}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.m.Validate(); !errors.Is(err, ErrMalformed) {
				t.Fatalf("got %v, want ErrMalformed", err)
			}
			r := mkReplica(t, 4, "b")
			if _, err := r.Step(tc.m); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Step got %v, want ErrMalformed: a commit arriving with a hundred nested view-changes "+
					"is an amplification attack on the verifier, and the cheapest place to stop it is before "+
					"anything expensive happens to it", err)
			}
		})
	}
}

func TestAWellFormedMessageOfEveryKindValidates(t *testing.T) {
	sig := []byte("s")
	pp := Message{Kind: KindPrePrepare, From: "b", View: 1, Seq: 2, Signature: sig}
	prep := Message{Kind: KindPrepare, From: "c", View: 1, Seq: 2, Signature: sig}
	cp := Message{Kind: KindCheckpoint, From: "b", Seq: 4, Signature: sig}
	vc := Message{Kind: KindViewChange, From: "a", View: 3, Seq: 4,
		Checkpoints: []Message{cp}, Prepared: []Certificate{{PrePrepare: pp, Prepares: []Message{prep}}}, Signature: sig}
	for _, m := range []Message{
		clientRequest("c", 1, "x"),
		pp, prep, cp, vc,
		{Kind: KindCommit, From: "a", View: 1, Seq: 2, Signature: sig},
		{Kind: KindNewView, From: "a", View: 3, ViewChanges: []Message{vc}, PrePrepares: []Message{pp},
			Epoch: 4, RotationDigest: Digest{1}, Signature: sig},
	} {
		if err := m.Validate(); err != nil {
			t.Fatalf("%s: %v", m.Kind, err)
		}
	}
}

func TestEveryKindNamesItselfForALogLine(t *testing.T) {
	for k, want := range map[Kind]string{
		KindRequest: "REQUEST", KindPrePrepare: "PRE-PREPARE", KindPrepare: "PREPARE",
		KindCommit: "COMMIT", KindCheckpoint: "CHECKPOINT", KindViewChange: "VIEW-CHANGE",
		KindNewView: "NEW-VIEW", Kind(42): "Kind(42)",
	} {
		if got := k.String(); got != want {
			t.Fatalf("Kind(%d) is %q, want %q", uint8(k), got, want)
		}
	}
}

func TestAnEquivocationProofIsCheckableBySomeoneWhoWasNotThere(t *testing.T) {
	a := signed("x", Message{Kind: KindPrePrepare, View: 1, Seq: 2, Digest: Digest{1}})
	b := signed("x", Message{Kind: KindPrePrepare, View: 1, Seq: 2, Digest: Digest{2}})
	if err := (Equivocation{A: a, B: b}).Verify(replicaVerifier()); err != nil {
		t.Fatalf("a genuine proof did not verify: %v", err)
	}
	for _, tc := range []struct {
		name string
		e    Equivocation
		want error
	}{
		{"two senders", Equivocation{A: a, B: signed("y", Message{Kind: KindPrePrepare, View: 1, Seq: 2, Digest: Digest{2}})}, ErrBadCertificate},
		{"no sender", Equivocation{A: Message{}, B: Message{}}, ErrBadCertificate},
		{"two kinds", Equivocation{A: a, B: signed("x", Message{Kind: KindPrepare, View: 1, Seq: 2, Digest: Digest{2}})}, ErrBadCertificate},
		{"two assignments", Equivocation{A: a, B: signed("x", Message{Kind: KindPrePrepare, View: 1, Seq: 3, Digest: Digest{2}})}, ErrBadCertificate},
		{"two identical votes", Equivocation{A: a, B: a}, ErrBadCertificate},
		{"unsigned", Equivocation{A: a, B: Message{Kind: KindPrePrepare, From: "x", View: 1, Seq: 2, Digest: Digest{3}}}, ErrBadSignature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.e.Verify(replicaVerifier()); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v — 'replica j equivocated' is precisely the claim an adversary would "+
					"most like believed about a correct replica", err, tc.want)
			}
		})
	}
	bad := Equivocation{A: a, B: b}
	bad.A.Signature = []byte("forged")
	if err := bad.Verify(replicaVerifier()); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature", err)
	}
}

func TestTheNullDigestIsTheOneThatExecutesAsNothing(t *testing.T) {
	if !(Digest{}).IsNull() {
		t.Fatal("the zero digest must be the null request")
	}
	if (Digest{1}).IsNull() {
		t.Fatal("a real digest is not null")
	}
}
