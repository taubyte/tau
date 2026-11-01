package cspbft

import (
	"fmt"
	"sort"
)

// The arithmetic of agreement. This file takes no Scorer: every function counts
// distinct senders, one vote each, so no change can weight a vote
// (TestTheCertificateFileNamesNoScorer).

// prepared is Castro-Liskov's prepared(v,n,d): a PRE-PREPARE and q-1 matching
// PREPAREs from distinct OTHER replicas — 2f of them when n is 3f+1.
//
// THE PRIMARY'S OWN PREPARE IS NOT COUNTED, because in the protocol the
// primary does not send one: its PRE-PREPARE is its vote, and counting both
// would let a faulty primary supply two of the q votes a quorum needs.
func prepared(e *entry, q int) bool {
	if e == nil || e.prePrepare == nil {
		return false
	}
	return countMatching(e.prepares, e.digest, e.prePrepare.From) >= q-1
}

// committedLocal is prepared plus q matching COMMITs from distinct replicas,
// THIS ONE INCLUDED. The self-inclusion is the difference between the two
// counts and it is not an off-by-one: a replica that has not itself committed
// has not made the assignment durable, so it may not execute.
func committedLocal(e *entry, q int) bool {
	if !prepared(e, q) {
		return false
	}
	return countMatching(e.commits, e.digest, "") >= q
}

func countMatching(votes map[ReplicaID]Message, d Digest, exclude ReplicaID) int {
	n := 0
	for from, m := range votes {
		if from == exclude {
			continue
		}
		if m.Digest == d {
			n++
		}
	}
	return n
}

// validateCertificate checks a prepared certificate somebody else asserts. It does
// not check that the PRE-PREPARE came from its view's primary: q-1 PREPAREs from
// distinct replicas include a correct one, which prepares only what its primary
// sent, and re-deriving the primary would make validity depend on the rotation.
func validateCertificate(c Certificate, q int, verify Verifier, known func(ReplicaID) bool) error {
	if err := c.validate(); err != nil {
		return err
	}
	pp := c.PrePrepare
	if !known(pp.From) {
		return fmt.Errorf("%w: a certificate's pre-prepare is from %q, which is not a replica here", ErrBadCertificate, pp.From)
	}
	if err := verify(pp.From, Preimage(pp), pp.Signature); err != nil {
		return fmt.Errorf("%w: a certificate's pre-prepare does not verify: %w", ErrBadCertificate, err)
	}
	seen := map[ReplicaID]bool{pp.From: true}
	for _, p := range c.Prepares {
		if p.View != pp.View || p.Seq != pp.Seq || p.Digest != pp.Digest {
			return fmt.Errorf("%w: a certificate holds a prepare for a different assignment", ErrBadCertificate)
		}
		if !known(p.From) {
			return fmt.Errorf("%w: a certificate holds a prepare from %q, which is not a replica here", ErrBadCertificate, p.From)
		}
		if seen[p.From] {
			return fmt.Errorf("%w: a certificate counts %q twice", ErrBadCertificate, p.From)
		}
		seen[p.From] = true
		if err := verify(p.From, Preimage(p), p.Signature); err != nil {
			return fmt.Errorf("%w: a certificate's prepare does not verify: %w", ErrBadCertificate, err)
		}
	}
	if len(c.Prepares) < q-1 {
		return fmt.Errorf("%w: a certificate carries %d prepares, and %d are needed", ErrBadCertificate, len(c.Prepares), q-1)
	}
	return nil
}

// validateCheckpointProof checks C: q CHECKPOINTs from distinct replicas, all
// for the same sequence and the same state. It returns the state digest they
// certify, which is what the rotation's epoch binds to.
//
// A proof for sequence zero is empty and that is not a hole: before the first
// checkpoint there is nothing to prove, and the low water mark is zero for
// everyone.
func validateCheckpointProof(ms []Message, seq Sequence, q int, verify Verifier, known func(ReplicaID) bool) (Digest, error) {
	if seq == 0 {
		if len(ms) != 0 {
			return nullDigest, fmt.Errorf("%w: a proof for sequence zero, which needs none", ErrBadCertificate)
		}
		return nullDigest, nil
	}
	if len(ms) < q {
		return nullDigest, fmt.Errorf("%w: a checkpoint proof of %d, and %d are needed", ErrBadCertificate, len(ms), q)
	}
	var d Digest
	seen := map[ReplicaID]bool{}
	for i, m := range ms {
		if m.Kind != KindCheckpoint || m.Seq != seq {
			return nullDigest, fmt.Errorf("%w: a checkpoint proof holds a %s for sequence %d", ErrBadCertificate, m.Kind, m.Seq)
		}
		if i == 0 {
			d = m.Digest
		} else if m.Digest != d {
			return nullDigest, fmt.Errorf("%w: a checkpoint proof certifies two different states", ErrBadCertificate)
		}
		if !known(m.From) {
			return nullDigest, fmt.Errorf("%w: a checkpoint proof holds %q, which is not a replica here", ErrBadCertificate, m.From)
		}
		if seen[m.From] {
			return nullDigest, fmt.Errorf("%w: a checkpoint proof counts %q twice", ErrBadCertificate, m.From)
		}
		seen[m.From] = true
		if err := verify(m.From, Preimage(m), m.Signature); err != nil {
			return nullDigest, fmt.Errorf("%w: a checkpoint proof does not verify: %w", ErrBadCertificate, err)
		}
	}
	return d, nil
}

// validateViewChange checks one VIEW-CHANGE whole: its checkpoint proof, and
// every prepared certificate in it. It is the expensive one — a VIEW-CHANGE
// carries up to L certificates of q-1 signatures each — and it is why the
// supported n range has a top.
func validateViewChange(m Message, q int, verify Verifier, known func(ReplicaID) bool) error {
	certified, err := validateCheckpointProof(m.Checkpoints, m.Seq, q, verify, known)
	if err != nil {
		return err
	}
	// The claimed state must be the certified one: a new view adopts it as the low water
	// mark, so a false digest makes lagging replicas discard their log for a checkpoint
	// nobody holds, and the lie propagates in their own later view-changes.
	if certified != m.Digest {
		return fmt.Errorf("%w: a view-change names a state at %d that its own checkpoint proof does not certify", ErrBadCertificate, m.Seq)
	}
	seqs := map[Sequence]bool{}
	for _, c := range m.Prepared {
		if c.PrePrepare.Seq <= m.Seq {
			return fmt.Errorf("%w: a view-change carries a certificate at or below its own checkpoint", ErrBadCertificate)
		}
		if seqs[c.PrePrepare.Seq] {
			return fmt.Errorf("%w: a view-change carries two certificates for sequence %d", ErrBadCertificate, c.PrePrepare.Seq)
		}
		seqs[c.PrePrepare.Seq] = true
		if c.PrePrepare.View >= m.View {
			return fmt.Errorf("%w: a view-change carries a certificate from the view it is moving to", ErrBadCertificate)
		}
		if err := validateCertificate(c, q, verify, known); err != nil {
			return err
		}
	}
	return nil
}

func sortedIDs[T any](m map[ReplicaID]T) []ReplicaID {
	out := make([]ReplicaID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// sortCertificates orders by sequence so that two replicas building the same
// VIEW-CHANGE build the same bytes. Map iteration order is not a detail here:
// the message is signed.
func sortCertificates(cs []Certificate) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].PrePrepare.Seq < cs[j].PrePrepare.Seq })
}
