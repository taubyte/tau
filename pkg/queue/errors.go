package queue

import (
	"errors"

	"github.com/taubyte/tau/pkg/raft"
)

var (
	// ErrNotLeader is returned by every mutating method when this node is not the
	// store's leader. It is raft.ErrNotLeader, so a caller may test for either.
	ErrNotLeader = raft.ErrNotLeader

	// ErrNoItem is returned by Claim when nothing is deliverable. The queue may
	// still hold items: they can be delayed, backing off, or already claimed.
	ErrNoItem = errors.New("queue: no deliverable item")

	// ErrNotFound is returned by Ack, Nack and Remove for an id the queue does
	// not hold.
	ErrNotFound = errors.New("queue: item not found")

	// ErrClaimed is returned by Remove for an item a consumer currently holds.
	// A claim is released by its holder's Ack or by its deadline, never by the
	// producer that pushed the item.
	ErrClaimed = errors.New("queue: item is claimed")

	// ErrStaleDelivery is returned by Ack and Nack when the delivery no longer
	// owns the item — its claim expired and the item was redelivered or
	// dead-lettered. The caller's work was not applied and must not be treated as
	// if it were.
	ErrStaleDelivery = errors.New("queue: delivery is stale")

	// ErrEmptyID is returned by Push for an empty item id.
	ErrEmptyID = errors.New("queue: item id must not be empty")

	// ErrForeignKey is returned when a key under this queue's prefix was not
	// written by this queue. It is a fault in whatever wrote it, so it is
	// reported rather than skipped.
	ErrForeignKey = errors.New("queue: unrecognized key under queue prefix")

	// ErrCorruptRecord is returned when a value under this queue's prefix does
	// not decode.
	ErrCorruptRecord = errors.New("queue: corrupt record")

	// ErrNoStore is returned by New when no store was given.
	ErrNoStore = errors.New("queue: store is required")

	// ErrNoName is returned by New when the configuration has no name.
	ErrNoName = errors.New("queue: name is required")

	// ErrSelfDeadLetter is returned by New when a queue is configured to
	// dead-letter into itself, which would redeliver an exhausted item forever.
	ErrSelfDeadLetter = errors.New("queue: dead-letter queue is this queue")

	// ErrInvalidConfig is returned by New for a negative duration, a negative
	// retry factor, or an apply timeout above raft.MaxApplyTimeout.
	ErrInvalidConfig = errors.New("queue: invalid configuration")

	// ErrNoClock is returned by WithClock(nil).
	ErrNoClock = errors.New("queue: clock is required")
)
