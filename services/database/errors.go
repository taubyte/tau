package database

import "errors"

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
var ErrGroupForming = errors.New("database: that database's raft group is still forming")

// ErrOutsideAllowance is a resource pinned to a region its scope does not
// permit. The compiler's audit reports a widening where a scope declares an
// allowance to check against; this is the same refusal at the one moment the
// region names are real, and it is an operator's to fix rather than a caller's.
var ErrOutsideAllowance = errors.New("database: that region is outside what this scope allows")
