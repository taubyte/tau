package cspbft

import "errors"

// errors.go is this package's vocabulary of refusals, in one place.
//
// The ones that look alike are kept apart on purpose, because they mean
// different things to whoever is reading a log line: ErrStaleView says the
// SENDER is behind and is completely normal during a view change; ErrFutureView
// says THIS replica is behind; ErrNotInConfiguration says the sender is not a
// replica here at all; ErrMalformed says the sender has a bug, or is attacking.
var (
	// ErrTooFewReplicas is returned by New for n < 4. Three replicas tolerate
	// zero faults, which is a majority protocol wearing a costume.
	ErrTooFewReplicas = errors.New("cspbft: fewer than four replicas tolerates no faults")

	// ErrDuplicateReplica is returned by New when one id appears twice. It is
	// refused rather than deduped silently, because a caller that built the set
	// from two sources has a bug that would otherwise show up as a quorum that
	// is one replica short of what it thinks.
	ErrDuplicateReplica = errors.New("cspbft: a replica id appears twice in the configuration")

	// ErrSelfNotInSet is returned by New when self is not one of the replicas.
	ErrSelfNotInSet = errors.New("cspbft: self is not in the replica set")

	// ErrNoSigner is returned by New when no Signer was given. A replica that
	// cannot sign cannot vote.
	ErrNoSigner = errors.New("cspbft: no signer")

	// ErrNoVerifier is returned by New when no Verifier was given. There is no
	// default, because a default that accepts everything is a consensus
	// protocol with no fault model.
	ErrNoVerifier = errors.New("cspbft: no verifier")

	// ErrNoClientVerifier is returned by New when no ClientVerifier was given.
	// It is separate from Verifier so a client identity can never be handed to
	// code that counts quorum members.
	ErrNoClientVerifier = errors.New("cspbft: no client verifier")

	// ErrInvalidInterval is returned by WithCheckpointInterval for K = 0.
	ErrInvalidInterval = errors.New("cspbft: invalid checkpoint interval")

	// ErrInvalidTimeout is returned by the timeout options for a
	// non-positive duration, or a maximum below the base.
	ErrInvalidTimeout = errors.New("cspbft: invalid timeout")

	// ErrMalformed is returned by Validate and by Step for a message carrying
	// fields that do not belong to its Kind. The caller drops the message and,
	// if it is counting, counts the sender.
	ErrMalformed = errors.New("cspbft: malformed message")

	// ErrBadSignature is returned by Step when the Verifier or ClientVerifier
	// refuses. The message never reaches the log: an unverified message must
	// never be able to influence non-equivocation state.
	ErrBadSignature = errors.New("cspbft: bad signature")

	// ErrNotInConfiguration is returned by Step for a sender this replica does
	// not know. It is not a fault of the sender's — a stale peer list is the
	// usual cause — so the caller drops the message and does not react.
	ErrNotInConfiguration = errors.New("cspbft: sender is not in the configuration")

	// ErrStaleView is returned by Step for a message from an older view. It is
	// NORMAL: a view change leaves messages in flight. The caller drops it.
	ErrStaleView = errors.New("cspbft: message is from an older view")

	// ErrFutureView is returned by Step for a message from a view this replica
	// has not entered. This replica is behind.
	//
	// THE CALLER MUST BUFFER THE MESSAGE AND RE-STEP IT once the view moves.
	// Dropping it is not merely lossy: a new primary broadcasts its NEW-VIEW
	// and then immediately assigns in that view, and a recipient that drops
	// the assignment because it lost the race with the NEW-VIEW will never be
	// sent it again. The cluster then waits for a replica that is waiting for
	// a message it already threw away.
	ErrFutureView = errors.New("cspbft: message is from a view not yet entered")

	// ErrOutOfWatermarks is returned by Step for a sequence number outside
	// (h, h+L]. It is how a faulty primary is stopped from exhausting the
	// sequence space.
	ErrOutOfWatermarks = errors.New("cspbft: sequence number is outside the water marks")

	// ErrNotPrimary is returned by Step for a PRE-PREPARE or NEW-VIEW from a
	// replica that is not the primary of that view.
	ErrNotPrimary = errors.New("cspbft: sender is not the primary of that view")

	// ErrDuplicateVote is returned by Step for a second message of one kind
	// from one sender for one (view, sequence) that says the same thing as the
	// first. A retransmission looks like this and is harmless.
	ErrDuplicateVote = errors.New("cspbft: sender already voted for that view and sequence")

	// ErrEquivocation is returned by Step when a sender's second message for
	// one (view, sequence) CONTRADICTS its first. An Evidence action carrying
	// the transferable proof is returned alongside it.
	ErrEquivocation = errors.New("cspbft: sender signed two conflicting messages")

	// ErrTooManyViewChanges is returned by Step for a VIEW-CHANGE from a
	// sender that is already asking for 2f+2 views above this one. It is the
	// water mark a VIEW-CHANGE does not otherwise have: a view is not a
	// sequence number, so nothing else bounds how far ahead one may name, and
	// every accepted one costs a journal record.
	ErrTooManyViewChanges = errors.New("cspbft: sender is asking for too many views at once")

	// ErrBadCertificate is returned by Step for a VIEW-CHANGE whose checkpoint
	// proof or prepared certificates do not hold up, and for a NEW-VIEW whose O
	// set is not what this replica recomputes from the same V.
	ErrBadCertificate = errors.New("cspbft: certificate does not hold up")

	// ErrRotationMismatch is returned by Step for a NEW-VIEW whose epoch or
	// rotation digest disagrees with this replica's. It is the one failure the
	// Scorer seam can cause, and it is named rather than experienced as a
	// stall.
	ErrRotationMismatch = errors.New("cspbft: new-view names a rotation this replica does not compute")

	// ErrScorerPermutation is carried by a Notice when a Scorer returned
	// something that is not a permutation of the configuration, or panicked.
	// The rotation falls back to round-robin and the protocol continues.
	ErrScorerPermutation = errors.New("cspbft: scorer did not return a permutation of the configuration")

	// ErrUnknownTimer is carried by a Notice from Timeout for a timer this
	// package never sets.
	ErrUnknownTimer = errors.New("cspbft: unknown timer")

	// ErrAlreadyStarted is returned by Restore after the first Step. A journal
	// replayed into a running replica would reorder history.
	ErrAlreadyStarted = errors.New("cspbft: replica has already stepped")

	// ErrCorruptRecord is returned by DecodeRecord and by Restore for a record
	// that does not decode. The caller's journal is damaged; it must not be
	// silently skipped, because the record that is missing may be the one that
	// says what this replica already voted for.
	ErrCorruptRecord = errors.New("cspbft: corrupt journal record")
)
