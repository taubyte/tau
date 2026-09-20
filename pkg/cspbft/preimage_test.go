package cspbft

import (
	"bytes"
	"errors"
	"testing"
)

func TestASignedPrepareCannotBeReplayedAsACommit(t *testing.T) {
	base := Message{From: "a", View: 3, Seq: 7, Digest: Digest{1, 2, 3}}
	prepare, commit := base, base
	prepare.Kind, commit.Kind = KindPrepare, KindCommit
	if bytes.Equal(Preimage(prepare), Preimage(commit)) {
		t.Fatal("a prepare and a commit for one assignment share a preimage: one signature would then be two votes")
	}
	r := mkReplica(t, 4, "b")
	req := clientRequest("c", 1, "one")
	mustStep(t, r, req)
	mustStep(t, r, signed("a", Message{Kind: KindPrePrepare, View: 0, Seq: 1, Digest: req.Digest}))

	stolen := signed("c", Message{Kind: KindPrepare, View: 0, Seq: 1, Digest: req.Digest})
	stolen.Kind = KindCommit // same signature, different claim
	if _, err := r.Step(stolen); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("got %v, want ErrBadSignature: the kind is the first byte of the preimage for exactly this reason", err)
	}
}

func TestNoTwoDistinctMessagesSharedAPreimage(t *testing.T) {
	inner := Message{Kind: KindPrepare, From: "a", View: 1, Seq: 1, Signature: []byte("s")}
	corpus := []Message{
		{Kind: KindPrepare, From: "a", View: 1, Seq: 1},
		{Kind: KindCommit, From: "a", View: 1, Seq: 1},
		{Kind: KindPrepare, From: "b", View: 1, Seq: 1},
		{Kind: KindPrepare, From: "a", View: 2, Seq: 1},
		{Kind: KindPrepare, From: "a", View: 1, Seq: 2},
		{Kind: KindPrepare, From: "a", View: 1, Seq: 1, Digest: Digest{1}},
		{Kind: KindRequest, Client: "c", Serial: 1, Proposal: Proposal("x")},
		{Kind: KindRequest, Client: "c", Serial: 2, Proposal: Proposal("x")},
		{Kind: KindRequest, Client: "c", Serial: 1, Proposal: Proposal("y")},
		{Kind: KindRequest, Client: "cc", Serial: 1, Proposal: Proposal("x")},
		// The pair that a byte separator would collide: "a\x00b" against "a"
		// and "b" in adjacent length-delimited fields.
		{Kind: KindRequest, Client: "a\x00b", Serial: 1, Proposal: Proposal("c")},
		{Kind: KindRequest, Client: "a", Serial: 1, Proposal: Proposal("b\x00c")},
		{Kind: KindViewChange, From: "a", View: 1, Checkpoints: []Message{inner}},
		{Kind: KindViewChange, From: "a", View: 1, ViewChanges: []Message{inner}},
		{Kind: KindViewChange, From: "a", View: 1, Prepared: []Certificate{{PrePrepare: inner}}},
		{Kind: KindViewChange, From: "a", View: 1, Prepared: []Certificate{{PrePrepare: inner, Prepares: []Message{inner}}}},
		{Kind: KindNewView, From: "a", View: 1, Epoch: 4},
		{Kind: KindNewView, From: "a", View: 1, Epoch: 5},
		{Kind: KindNewView, From: "a", View: 1, Epoch: 4, RotationDigest: Digest{9}},
	}
	seen := map[string]int{}
	for i, m := range corpus {
		k := string(Preimage(m))
		if j, dup := seen[k]; dup {
			t.Fatalf("corpus entries %d and %d share a preimage", j, i)
		}
		seen[k] = i
	}
}

func TestASignatureIsNeverOverTheBytesThatArrived(t *testing.T) {
	// Two encodings of one value must verify identically, which is only true
	// if the preimage is canonical rather than whatever the wire produced.
	a := Message{Kind: KindPrepare, From: "a", View: 1, Seq: 1}
	b := a
	b.Proposal = Proposal{} // an empty non-nil slice
	b.Checkpoints = []Message{}[:0]
	if !bytes.Equal(Preimage(a), Preimage(b)) {
		t.Fatal("a nil slice and an empty slice produced different preimages: a codec that round-trips one to the other would break every signature")
	}
}

func TestAJournalRecordSurvivesARoundTrip(t *testing.T) {
	inner := signed("a", Message{Kind: KindPrepare, View: 1, Seq: 2, Digest: Digest{4}})
	pp := signed("a", Message{Kind: KindPrePrepare, View: 1, Seq: 2, Digest: Digest{4}})
	for _, r := range []Record{
		{Kind: RecordMessage, Message: signed("a", Message{Kind: KindCommit, View: 1, Seq: 2, Digest: Digest{4}})},
		{Kind: RecordMessage, Message: signed("a", Message{
			Kind: KindViewChange, View: 3, Seq: 8, Digest: Digest{5},
			Checkpoints: []Message{signed("b", Message{Kind: KindCheckpoint, Seq: 8, Digest: Digest{5}})},
			Prepared:    []Certificate{{PrePrepare: pp, Prepares: []Message{inner}}},
		})},
		{Kind: RecordMessage, Message: clientRequest("c", 9, "payload")},
		{Kind: RecordViewEntered, View: 12},
		{Kind: RecordCheckpointStable, Seq: 8, Digest: Digest{5}},
	} {
		got, err := DecodeRecord(r.Encode())
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !bytes.Equal(got.Encode(), r.Encode()) {
			t.Fatalf("a record did not survive a round trip:\n got %+v\nwant %+v", got, r)
		}
		if got.Message.Signature != nil && !bytes.Equal(got.Message.Signature, r.Message.Signature) {
			t.Fatal("the signature did not survive, so a restored replica could not put the message back in a view-change")
		}
	}
}

func TestACorruptJournalRecordIsRefusedRatherThanSkipped(t *testing.T) {
	good := Record{Kind: RecordMessage, Message: signed("a", Message{Kind: KindCommit, View: 1, Seq: 2})}.Encode()
	for _, tc := range []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"wrong domain", append([]byte("cspbft/other/v1\x00"), good[len(recordDomain):]...)},
		{"truncated", good[:len(good)-4]},
		{"trailing bytes", append(append([]byte(nil), good...), 0)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRecord(tc.b); !errors.Is(err, ErrCorruptRecord) {
				t.Fatalf("got %v, want ErrCorruptRecord: the record that will not decode may be the one that says "+
					"what this replica already voted for", err)
			}
		})
	}
	r := mkReplica(t, 4, "b")
	if _, err := r.Restore([]Record{{Kind: RecordKind(99)}}); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("got %v, want ErrCorruptRecord for an unknown record kind", err)
	}
	r2 := mkReplica(t, 4, "b")
	bad := Record{Kind: RecordMessage, Message: Message{Kind: KindCommit, View: 1, Seq: 1}}
	if _, err := r2.Restore([]Record{bad}); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("got %v, want ErrCorruptRecord for a record holding a message that does not validate", err)
	}
}

func TestADamagedLengthPrefixDoesNotDecideHowMuchMemoryToAllocate(t *testing.T) {
	good := Record{Kind: RecordMessage, Message: signed("a", Message{Kind: KindCommit, View: 1, Seq: 2})}.Encode()
	damaged := append([]byte(nil), good...)
	// Overwrite the inner message's length prefix with something enormous.
	for i := len(recordDomain); i+8 <= len(damaged); i++ {
		damaged[i] = 0xff
	}
	if _, err := DecodeRecord(damaged); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("got %v, want ErrCorruptRecord", err)
	}
}
