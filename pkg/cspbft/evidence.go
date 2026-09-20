package cspbft

import "fmt"

// evidence.go is the only thing in this package that produces a fact about a
// replica rather than about the protocol.
//
// AN EQUIVOCATION IS THE ONLY SOUND INPUT TO A PUNISHMENT. Everything else a
// replica might hold against a peer — it was slow, it went quiet, it missed a
// view — is local observation, and asynchrony is indistinguishable from
// failure, so acting on it lets a network hiccup evict a correct replica.
// Two conflicting messages under one signature are different in kind: anyone
// can check them, including someone who was not there.
//
// This package DETECTS equivocation and hands out the proof. It does not feed
// it back into the rotation, because whether replica i has SEEN a proof is
// local, and a rotation driven by locally-held proofs diverges. The caller
// closes the loop by submitting the proof as an ordinary proposal: once it
// commits, every replica's application state holds it and their Scorer reads
// it from an agreed StateDigest.

// Equivocation is transferable proof that one replica signed two conflicting
// messages for one (view, sequence).
type Equivocation struct{ A, B Message }

// Verify checks the proof from scratch, the way a third party who was not
// present must: same sender, same kind, same view and sequence, different
// digests, and both signatures good.
//
// A CALLER MUST RUN THIS BEFORE ACTING ON AN Evidence IT DID NOT PRODUCE
// ITSELF. A proof that arrives over the network is a claim until it verifies,
// and "replica j equivocated" is precisely the claim an adversary would most
// like to be believed about a correct replica.
func (e Equivocation) Verify(v Verifier) error {
	if e.A.From == "" || e.A.From != e.B.From {
		return fmt.Errorf("%w: an equivocation naming two different senders", ErrBadCertificate)
	}
	if e.A.Kind != e.B.Kind {
		return fmt.Errorf("%w: an equivocation across two kinds, %s and %s", ErrBadCertificate, e.A.Kind, e.B.Kind)
	}
	if e.A.View != e.B.View || e.A.Seq != e.B.Seq {
		return fmt.Errorf("%w: an equivocation across two assignments", ErrBadCertificate)
	}
	if e.A.Digest == e.B.Digest {
		// Two identical votes are a retransmission, which is normal and which
		// an adversary would be delighted to have counted as a fault.
		return fmt.Errorf("%w: an equivocation whose two messages agree", ErrBadCertificate)
	}
	if err := v(e.A.From, Preimage(e.A), e.A.Signature); err != nil {
		return fmt.Errorf("%w: the first message does not verify: %w", ErrBadSignature, err)
	}
	if err := v(e.B.From, Preimage(e.B), e.B.Signature); err != nil {
		return fmt.Errorf("%w: the second message does not verify: %w", ErrBadSignature, err)
	}
	return nil
}

// Offender is who the proof incriminates.
func (e Equivocation) Offender() ReplicaID { return e.A.From }
