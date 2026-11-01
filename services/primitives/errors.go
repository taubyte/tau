package primitives

import (
	"errors"
	"fmt"
)

// Named refusals; status.go is the one table turning them into statuses and back.

var (
	// ErrNoHost is a service with nothing to answer on.
	ErrNoHost = errors.New("primitives: no host to answer on")

	// ErrNoRegister is a service that cannot read declarations. A service that
	// trusted its callers to describe the queue would be a service any caller
	// could point at somebody else's — see the package doc.
	ErrNoRegister = errors.New("primitives: nothing to read declarations from")

	// ErrNoConsensus is a service composed with no way to open a raft group.
	// Both of this kind's types are decisions about who writes next, so a node
	// that cannot hold an election can serve neither.
	ErrNoConsensus = errors.New("primitives: nothing to open a consensus group in")

	// ErrNoRecords is a service composed with nowhere to keep a log's records.
	// A queue would still work; saying so at construction is better than
	// answering half the calls.
	ErrNoRecords = errors.New("primitives: nothing to keep log records in")

	// ErrHalfAClock is a service given one of the clock's two halves. Which
	// schedules exist and how a fire reaches its workload are both the
	// composer's, and a service holding one without the other either claims
	// every slot in the cloud and delivers nothing or runs a pass over nothing
	// — see WithSchedules and WithFire.
	ErrHalfAClock = errors.New("primitives: a clock with schedules and no way to fire one, or the other way round")

	// ErrNoClock is a window handed to a node that holds the namespace and
	// runs no compute. There is no deployment view to enumerate crons from, so
	// it can walk nothing — see WithSchedules. It is REFUSED and never answered
	// empty: a tasking the leader believes was done is a minute nobody fires.
	ErrNoClock = errors.New("primitives: this node runs no clock")

	// ErrNoProject, ErrNoEnvironment and ErrNoID are an address with a hole in
	// it. They are separate so a caller is told which part it left out.
	ErrNoProject     = errors.New("primitives: no project")
	ErrNoEnvironment = errors.New("primitives: no environment")
	ErrNoID          = errors.New("primitives: no resource id")

	// ErrNotDeclared is an id this deployment does not name. It is NotFound and
	// never an empty answer: a caller handed nothing for a queue that does not
	// exist would believe it had sent work nobody will ever run.
	ErrNotDeclared = errors.New("primitives: this deployment declares no such resource")

	// ErrNotASync is an id that names something else — a database, a bucket.
	ErrNotASync = errors.New("primitives: that id is not a sync resource")

	// ErrWrongType is a queue call on a log, or the other way round. The two
	// types are two operation surfaces and a log has nothing to claim. It is
	// the CALLER's mistake, which is why it is told apart from the one below.
	ErrWrongType = errors.New("primitives: that resource is not of that type")

	// ErrTypeUnserved is a `sync` type this service does not serve YET. An
	// operator reading "that resource is not a queue" about their lock would go
	// looking for a config mistake they did not make.
	ErrTypeUnserved = errors.New("primitives: this service does not serve that sync type yet")

	// ErrConsensusUnserved is a resolution this service does not keep, refused by name
	// rather than served weaker than written.
	ErrConsensusUnserved = errors.New("primitives: this service does not keep that consensus")

	// ErrInstanceUnserved is a Ref naming one instance of many. The `sync` kind has no
	// `instances:` block, so no project can declare one; see address.valid.
	ErrInstanceUnserved = errors.New("primitives: this service does not serve one instance of many yet")

	// ErrNoItemID is a send with no identity. The id is what makes a repeated
	// send a no-op instead of a second copy of the work, so a queue cannot
	// invent one.
	ErrNoItemID = errors.New("primitives: no item id")

	// ErrTooLarge is one record or one item bigger than this service takes. It
	// is the ABI's ceiling rather than a policy: the bytes cross as one
	// list<u8> and are materialised whole on both sides, and a queue item lives
	// inline in a state machine every voter holds in memory.
	ErrTooLarge = errors.New("primitives: that is larger than this service accepts")

	// ErrBadCursor is a position this log never issued, or one whose records
	// retention has already dropped. Its own arm because a caller FIXES it — by
	// starting from the beginning — rather than retrying it.
	ErrBadCursor = errors.New("primitives: that is not a position this log can read from")

	// ErrNoSuchDelivery is a settle for an item the queue does not hold.
	ErrNoSuchDelivery = errors.New("primitives: this queue holds no such item")

	// ErrStaleDelivery is a settle whose claim the queue no longer recognises:
	// it expired and the item was redelivered or dead-lettered. The caller's
	// work was NOT applied and must not be treated as if it were.
	ErrStaleDelivery = errors.New("primitives: that delivery is stale")
)

// ErrForming is a call that arrived before this node's consensus formed: a retry,
// never a miss. The group forms in the background rather than blocking a request on
// raft.New's bootstrap timeout.
var ErrForming = errors.New("primitives: this node's consensus is still forming")

// ErrClosed is a call on a node that is shutting down. It is NOT ErrForming,
// which is the distinction a caller acts on: told "forming" it retries here and
// waits for something that will never come.
var ErrClosed = errors.New("primitives: this node has closed its consensus")

// ErrNotLeading is a write on a node that does not hold this namespace's
// leadership. It is a RETRY: one node at a time may write, and which one is a
// fact the caller can act on rather than a failure of the resource.
var ErrNotLeading = errors.New("primitives: this node is not the one that may write")

// NotLeading is that refusal with the leader's name on it. The service names the
// leader and does not dial it: forwarding would be a second write path.
type NotLeading struct{ Leader string }

func (e NotLeading) Error() string {
	if e.Leader == "" {
		return ErrNotLeading.Error() + ": and no node leads it yet"
	}
	return ErrNotLeading.Error() + ": " + e.Leader + " does"
}

func (e NotLeading) Unwrap() error { return ErrNotLeading }

// tooLarge names the ceiling that was crossed, so the answer is actionable
// rather than only a refusal.
func tooLarge(what string, got, ceiling int) error {
	return fmt.Errorf("%w: %s is %d bytes and the ceiling is %d", ErrTooLarge, what, got, ceiling)
}
