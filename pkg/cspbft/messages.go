package cspbft

import "fmt"

// ReplicaID is a replica's identity. It is a libp2p peer id in every caller so
// far, but this package only ever compares, sorts and hashes it, so it is a
// string — the same argument pkg/placement makes about a Member.
//
// AN ID IS NOT A KEY. Key material, rotation and distribution live entirely
// behind Verifier. This package has no keystore.
type ReplicaID string

// ClientID is whoever submits proposals. It is a DIFFERENT type from ReplicaID
// so that a client identity can never be passed to code that counts quorum
// members: the mistake is a compile error.
type ClientID string

// View is a configuration of the protocol with one primary. It only ever
// increases.
type View uint64

// Sequence is the position of a proposal in the total order.
type Sequence uint64

// Digest is what replicas actually agree about. Proposals are opaque, so
// everything downstream of the client compares digests.
type Digest [32]byte

// Proposal is the payload being ordered. This package never looks inside one:
// a caller batching a hundred requests into one Proposal is indistinguishable
// from a caller submitting one.
type Proposal []byte

// Kind is which protocol message this is. It is the first byte of the signing
// preimage, which is what stops a PREPARE being replayed as a COMMIT.
type Kind uint8

// The protocol messages.
const (
	KindRequest Kind = iota + 1
	KindPrePrepare
	KindPrepare
	KindCommit
	KindCheckpoint
	KindViewChange
	KindNewView
)

func (k Kind) String() string {
	switch k {
	case KindRequest:
		return "REQUEST"
	case KindPrePrepare:
		return "PRE-PREPARE"
	case KindPrepare:
		return "PREPARE"
	case KindCommit:
		return "COMMIT"
	case KindCheckpoint:
		return "CHECKPOINT"
	case KindViewChange:
		return "VIEW-CHANGE"
	case KindNewView:
		return "NEW-VIEW"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

// Message is every protocol message, as one struct with a Kind.
//
// IT IS ONE STRUCT BECAUSE THE CALLER OWNS THE WIRE, and one struct is one
// codec registration rather than seven. Validate is what keeps that from
// meaning "any field may hold anything": a field that does not belong to the
// Kind is ErrMalformed, checked before the message reaches any log.
//
// Note what is not here: no timestamp, no deadline, no sender-asserted
// anything beyond the vote itself. TestNoProtocolMessageCarriesAClock is the
// census that keeps it that way.
type Message struct {
	Kind   Kind
	From   ReplicaID // empty on a KindRequest, which a client signs
	View   View
	Seq    Sequence
	Digest Digest

	Client   ClientID // KindRequest
	Serial   uint64   // KindRequest — a per-client counter, NOT a clock
	Proposal Proposal // KindRequest

	Checkpoints []Message     // KindViewChange: C, Quorum(n) CHECKPOINTs
	Prepared    []Certificate // KindViewChange: P
	ViewChanges []Message     // KindNewView:    V, Quorum(n) VIEW-CHANGEs
	PrePrepares []Message     // KindNewView:    O

	Epoch          Sequence // KindNewView: the checkpoint the rotation is bound to
	RotationDigest Digest   // KindNewView: H(perm), which makes a disagreement detectable

	Signature []byte
}

// Certificate is a prepared certificate: a PRE-PREPARE and the matching
// PREPAREs that make it prepared.
//
// It is a named type rather than two loose fields so that an aggregate or
// threshold signature can one day replace Quorum(n)-1 individual signatures
// without the state machine changing shape. That is the change that takes a
// large configuration's view change from seconds to milliseconds, and keeping
// the seam open costs nothing today.
type Certificate struct {
	PrePrepare Message
	Prepares   []Message
}

// nullDigest is the digest of the null request the new primary uses to fill a
// gap in the O set. It executes as nothing.
var nullDigest Digest

// IsNull reports whether d is the null request's digest.
func (d Digest) IsNull() bool { return d == nullDigest }

// Validate refuses a message carrying fields that do not belong to its Kind.
//
// IT RUNS BEFORE VERIFICATION AND BEFORE THE LOG. A COMMIT arriving with a
// hundred nested VIEW-CHANGEs attached is an amplification attack on the
// verifier, not a protocol message, and the cheapest place to stop it is
// before anything expensive happens to it.
func (m Message) Validate() error {
	if len(m.Signature) == 0 {
		return fmt.Errorf("%w: %s carries no signature", ErrMalformed, m.Kind)
	}
	hasNested := len(m.Checkpoints) > 0 || len(m.Prepared) > 0 || len(m.ViewChanges) > 0 || len(m.PrePrepares) > 0
	hasClient := m.Client != "" || m.Serial != 0 || len(m.Proposal) > 0
	hasRotation := m.Epoch != 0 || m.RotationDigest != nullDigest

	switch m.Kind {
	case KindRequest:
		// A request is signed by a client, so From is empty and there is no
		// view or sequence yet: assigning one is the primary's job.
		if m.From != "" {
			return fmt.Errorf("%w: a request names a replica in From", ErrMalformed)
		}
		if m.Client == "" {
			return fmt.Errorf("%w: a request names no client", ErrMalformed)
		}
		if m.View != 0 || m.Seq != 0 {
			return fmt.Errorf("%w: a request carries a view or sequence", ErrMalformed)
		}
		if hasNested || hasRotation {
			return fmt.Errorf("%w: a request carries nested messages", ErrMalformed)
		}
	case KindPrePrepare, KindPrepare, KindCommit:
		if err := m.requireReplica(); err != nil {
			return err
		}
		if m.Seq == 0 {
			return fmt.Errorf("%w: %s carries sequence zero", ErrMalformed, m.Kind)
		}
		if hasClient || hasNested || hasRotation {
			return fmt.Errorf("%w: %s carries fields of another kind", ErrMalformed, m.Kind)
		}
	case KindCheckpoint:
		if err := m.requireReplica(); err != nil {
			return err
		}
		if m.View != 0 {
			return fmt.Errorf("%w: a checkpoint carries a view", ErrMalformed)
		}
		if hasClient || hasNested || hasRotation {
			return fmt.Errorf("%w: a checkpoint carries fields of another kind", ErrMalformed)
		}
	case KindViewChange:
		if err := m.requireReplica(); err != nil {
			return err
		}
		if m.View == 0 {
			return fmt.Errorf("%w: a view-change to view zero", ErrMalformed)
		}
		if hasClient || len(m.ViewChanges) > 0 || len(m.PrePrepares) > 0 || hasRotation {
			return fmt.Errorf("%w: a view-change carries fields of another kind", ErrMalformed)
		}
		for _, c := range m.Prepared {
			if err := c.validate(); err != nil {
				return err
			}
		}
		for _, c := range m.Checkpoints {
			if c.Kind != KindCheckpoint {
				return fmt.Errorf("%w: a view-change's C holds a %s", ErrMalformed, c.Kind)
			}
			if err := c.Validate(); err != nil {
				return err
			}
		}
	case KindNewView:
		if err := m.requireReplica(); err != nil {
			return err
		}
		if m.View == 0 {
			return fmt.Errorf("%w: a new-view for view zero", ErrMalformed)
		}
		if hasClient || len(m.Checkpoints) > 0 || len(m.Prepared) > 0 {
			return fmt.Errorf("%w: a new-view carries fields of another kind", ErrMalformed)
		}
		for _, v := range m.ViewChanges {
			if v.Kind != KindViewChange {
				return fmt.Errorf("%w: a new-view's V holds a %s", ErrMalformed, v.Kind)
			}
			if err := v.Validate(); err != nil {
				return err
			}
		}
		for _, p := range m.PrePrepares {
			if p.Kind != KindPrePrepare {
				return fmt.Errorf("%w: a new-view's O holds a %s", ErrMalformed, p.Kind)
			}
			if err := p.Validate(); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: unknown kind %d", ErrMalformed, uint8(m.Kind))
	}
	return nil
}

func (m Message) requireReplica() error {
	if m.From == "" {
		return fmt.Errorf("%w: %s names no sender", ErrMalformed, m.Kind)
	}
	if m.Client != "" {
		return fmt.Errorf("%w: %s names a client", ErrMalformed, m.Kind)
	}
	return nil
}

func (c Certificate) validate() error {
	if c.PrePrepare.Kind != KindPrePrepare {
		return fmt.Errorf("%w: a certificate's pre-prepare is a %s", ErrMalformed, c.PrePrepare.Kind)
	}
	if err := c.PrePrepare.Validate(); err != nil {
		return err
	}
	for _, p := range c.Prepares {
		if p.Kind != KindPrepare {
			return fmt.Errorf("%w: a certificate holds a %s among its prepares", ErrMalformed, p.Kind)
		}
		if err := p.Validate(); err != nil {
			return err
		}
	}
	return nil
}
