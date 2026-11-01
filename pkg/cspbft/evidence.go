package cspbft

import "fmt"

// An equivocation (two conflicting messages under one signature) is the only sound
// input to a punishment: anyone can check it, while slowness or silence is
// indistinguishable from asynchrony. See the package doc for how a caller closes the
// loop.

// Equivocation is transferable proof that one replica signed two conflicting
// messages for one (view, sequence).
type Equivocation struct{ A, B Message }

// Verify checks the proof from scratch: same sender, kind, view and sequence,
// different digests, both signatures good. Run it before acting on Evidence this
// replica did not produce.
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
