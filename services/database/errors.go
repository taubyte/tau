package database

import (
	"errors"
	"fmt"
)

// errors.go is this package's vocabulary of refusals, in one place so the wire
// mapping in rpc.go is a table over named things rather than string matching.

var (
	// ErrNoHost is a service with nothing to answer on.
	ErrNoHost = errors.New("database: no host to answer on")

	// ErrNoRegister is a service that cannot read declarations. A database
	// service that trusted its callers to describe the database would be a
	// service any caller could point somewhere else — see the proto's file doc.
	ErrNoRegister = errors.New("database: nothing to read declarations from")

	// ErrNoEngines is a service with no way to open anything.
	ErrNoEngines = errors.New("database: no engines registered")

	// ErrNoProject, ErrNoEnvironment and ErrNoID are an address with a hole in
	// it. They are separate so a caller is told which part it left out.
	ErrNoProject     = errors.New("database: no project")
	ErrNoEnvironment = errors.New("database: no environment")
	ErrNoID          = errors.New("database: no database id")

	// ErrNotDeclared is an id this deployment does not name. It is NotFound and
	// never an empty answer: a caller handed nothing for a database that does
	// not exist would write into a store that is not there.
	ErrNotDeclared = errors.New("database: this deployment declares no such database")

	// ErrNotADatabase is an id that names something else — a cache, a bucket.
	ErrNotADatabase = errors.New("database: that id is not a database")

	// ErrWrongModel is a kv call on a database the author declared as something
	// else. The four models are four operation surfaces and a sql store has no
	// key to get. It is the CALLER's mistake.
	ErrWrongModel = errors.New("database: that database is not a kv")

	// ErrModelUnserved is a model this service does not serve YET, which is a
	// different sentence from the one above and is told apart on purpose: one
	// says the caller used the wrong surface, the other says this cloud has not
	// implemented that model. An operator reading "that database is not a kv"
	// about their postgres would go looking for a config mistake they did not
	// make.
	ErrModelUnserved = errors.New("database: this service does not serve that model yet")

	// ErrConsensusUnserved is a database whose author asked for a resolution
	// this service does not keep. It is refused BY NAME rather than served
	// weaker than written — the same rule node/queue.go keeps for a crdt queue
	// and node/cache.go for a cache, and the one the database kind has been
	// missing while `consensus` was RequiredWhen and read by nobody.
	ErrConsensusUnserved = errors.New("database: this service does not keep that consensus")

	// ErrNoKey is an operation with nothing to operate on.
	ErrNoKey = errors.New("database: no key")

	// ErrReleasing is a write to a database this node is in the act of letting
	// go of: the gate has been cleared and the bytes are being deleted. It is a
	// RETRY and not a failure — the copy is moving and the node taking it over
	// holds the same rows — which is why it is Unavailable on the wire.
	//
	// IT IS THE SEALED WINDOW ONLY. A node that refused writes for the whole of
	// a release would be an outage per sweep; everything before the seal is
	// accepted and makes the release ask again instead.
	ErrReleasing = errors.New("database: this node is releasing that database")

	// ErrNotSet is a key nobody wrote. It is its own arm rather than an empty
	// value, for the reason the secrets service gives: a workload handed "" for
	// what it stored fails later, looking like a bug in the workload.
	ErrNotSet = errors.New("database: no such key")
)

// ErrGroupForming is a raft database reached before its group has formed. It is
// a RETRY and not a failure, which is why it is its own arm: a node that has
// just started is a node whose next request will do better.
//
// IT IS NEVER A MISS AND NEVER A WAIT. A ledger's reader acts on "no such key",
// so answering one for a group that has not formed is worse than refusing; and
// waiting is the thirty seconds raft.New spends looking for peers, which is the
// gateway timeout node/queue.go was written to stop.
var ErrGroupForming = errors.New("database: that database's raft group is still forming")

// ErrNotLeading is a raft database reached on a node that is not its group's
// leader. It is a RETRY: one node at a time may answer for a group, and which
// one is a fact the caller can act on rather than a failure of the database.
var ErrNotLeading = errors.New("database: this node does not lead that database's raft group")

// NotLeading is that refusal with the leader's name on it, which is what lets
// one operation be sent on instead of handed back.
//
// THE STORE NAMES THE LEADER AND DOES NOT DIAL IT. A store that forwarded would
// be a second write path with its own failure modes, reaching back up through
// the service that composed it — node/queue.go's rule, kept. What is different
// here is that this service HAS somewhere to send it, so the refusal carries
// the address instead of being the end of the call.
type NotLeading struct{ Leader string }

func (e NotLeading) Error() string {
	if e.Leader == "" {
		return ErrNotLeading.Error() + ": and no node leads it yet"
	}
	return ErrNotLeading.Error() + ": " + e.Leader + " does"
}

func (e NotLeading) Unwrap() error { return ErrNotLeading }

// ErrNotHere is a raft database this node is not a voter for and cannot name a
// node that is. Opening the group would make this node a voter BECAUSE
// SOMEBODY ASKED, which is the same act Holdings.Held refuses for a crdt copy,
// so it refuses instead and the caller retries elsewhere.
var ErrNotHere = errors.New("database: another node holds that database")

// ErrUnreachable is a voter that did not answer at all -- a peer that cannot be
// decoded, dialled, or that answered something no node of this service wrote.
//
// IT WRAPS ErrNotHere BECAUSE IT IS THE SAME INSTRUCTION AND A DIFFERENT FACT.
// A call walking the voters continues past both, which is the whole reason the
// walk exists: one machine going would otherwise take every ledger call made
// through every node outside the group with it. What it must NOT do is continue
// past a refusal the group itself wrote -- that is the group talking, and the
// next voter would only say it again.
var ErrUnreachable = fmt.Errorf("%w: and it could not be reached", ErrNotHere)

// ErrGroupClosed is a raft database on a node that is shutting down: it has
// closed its groups and will open no more.
//
// IT IS NOT ErrGroupForming, which is the distinction the caller acts on. A
// node told a group is forming retries HERE and waits for one that will never
// form; told this, it asks another voter. Same code on the wire, different
// sentence and a different place for the next call to land.
var ErrGroupClosed = fmt.Errorf("%w: this node has closed its raft groups", ErrNotHere)

// ErrOutsideAllowance is a resource pinned to a region its scope does not
// permit. The compiler's audit reports a widening where a scope declares an
// allowance to check against; this is the same refusal at the one moment the
// region names are real, and it is an operator's to fix rather than a caller's.
var ErrOutsideAllowance = errors.New("database: that region is outside what this scope allows")
