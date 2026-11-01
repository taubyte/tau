package database

import (
	"errors"
	"fmt"
)

// Named refusals, so rpc.go's wire mapping is a table rather than string matching.

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

	// ErrModelUnserved is a model this service does not serve yet, told apart from
	// ErrWrongModel so an operator does not hunt a config mistake they did not make.
	ErrModelUnserved = errors.New("database: this service does not serve that model yet")

	// ErrConsensusUnserved is a resolution this service does not keep, refused by name
	// rather than served weaker than written.
	ErrConsensusUnserved = errors.New("database: this service does not keep that consensus")

	// ErrNoKey is an operation with nothing to operate on.
	ErrNoKey = errors.New("database: no key")

	// ErrReleasing is a write during the sealed window of a release: a retry, since the
	// node taking over holds the same rows. Only the sealed window refuses; earlier
	// writes make the release ask again.
	ErrReleasing = errors.New("database: this node is releasing that database")

	// ErrNotSet is a key nobody wrote. It is its own arm rather than an empty
	// value, for the reason the secrets service gives: a workload handed "" for
	// what it stored fails later, looking like a bug in the workload.
	ErrNotSet = errors.New("database: no such key")
)

// ErrGroupForming is a raft database reached before its group formed: a retry. Never
// a miss (a ledger's reader acts on "no such key") and never a wait (raft.New's
// thirty seconds).
var ErrGroupForming = errors.New("database: that database's raft group is still forming")

// ErrNotLeading is a raft database reached on a node that is not its group's
// leader. It is a RETRY: one node at a time may answer for a group, and which
// one is a fact the caller can act on rather than a failure of the database.
var ErrNotLeading = errors.New("database: this node does not lead that database's raft group")

// NotLeading is that refusal with the leader's name on it, so the call can be sent on.
// The store names the leader and never dials it.
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

// ErrUnreachable is a voter that did not answer at all. It wraps ErrNotHere so a call
// walking the voters continues past it; a refusal the group itself wrote ends the
// walk.
var ErrUnreachable = fmt.Errorf("%w: and it could not be reached", ErrNotHere)

// ErrGroupClosed is a raft database on a node shutting down. Unlike ErrGroupForming
// the caller should ask another voter, not retry here.
var ErrGroupClosed = fmt.Errorf("%w: this node has closed its raft groups", ErrNotHere)
