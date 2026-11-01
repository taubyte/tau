package storage

import (
	"context"
	"errors"
	"sync"

	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/register"
)

// R and K come from the published fleet document. R must be computed with
// identically or nodes disagree about placement forever; K is what the cloud
// promises, and a per-node K drifts invisibly (a bring-up node left at 1 acks writes
// alone).
//
//	NOT PUBLISHED   the defaults, which every node reaches identically
//	UNREACHABLE     keep the last values read
//	NEVER READ      R: additive-only. Serve, open shards, import, collect dead
//	                objects (gates 1-4 never mention R), but never drop a
//	                misplaced block (gate 5 compares against R).
//	                K: fail-closed. A put that cannot learn K fails.

// fleetSource is the slice of a register this needs. It is asserted rather than
// required of register.Registry, because a consumer that only resolves projects
// is a legitimate thing to build a service over.
type fleetSource interface {
	ResolveFleet(ctx context.Context) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// fleetPolicy reads and remembers what the cloud promises about objects. Not shared
// with services/database's fleetFloor: what an unknown value authorises differs.
type fleetPolicy struct {
	from fleetSource

	mu       sync.Mutex
	known    bool
	at       register.ObjectID
	revision uint64
	replicas int
	barrier  int
}

// read answers R and K from one document at one revision, and whether this node has
// ever learned them.
func (f *fleetPolicy) read(ctx context.Context) (replicas, barrier int, known bool) {
	if f == nil || f.from == nil {
		// A SERVICE COMPOSED WITHOUT A FLEET SOURCE knows the defaults and
		// nothing else — which is agreed, because every such node reaches them
		// the same way.
		return DefaultReplicas, DefaultBarrier, true
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	pointer, err := f.from.ResolveFleet(ctx)
	switch {
	case errors.Is(err, register.ErrNotPublished):
		// A DEFINITIVE ABSENCE IS STILL AGREEMENT: every node reads the same
		// nothing and reaches the same defaults.
		f.known, f.replicas, f.barrier = true, DefaultReplicas, DefaultBarrier
		return f.replicas, f.barrier, true
	case err != nil:
		// Unreachable changes nothing, and says so if nothing was ever read.
		return f.replicas, f.barrier, f.known
	}

	if f.known && pointer.Object == f.at {
		return f.replicas, f.barrier, true
	}

	raw, err := f.from.Object(ctx, pointer.Object)
	if err != nil {
		return f.replicas, f.barrier, f.known
	}
	document, err := fleet.Decode(raw)
	if err != nil {
		// A CORRUPT DECLARATION IS NOT SILENCE. Falling back to the defaults
		// here would have this node place differently from one that read the
		// document before it broke — the disagreement the pointer exists to
		// end. It keeps what it had, and has nothing if it had nothing.
		return f.replicas, f.barrier, f.known
	}
	promised, err := document.Storage.Resolved(DefaultReplicas, DefaultBarrier)
	if err != nil {
		// A barrier above the replica target would fail every write, so the document is
		// refused like one that will not parse.
		return f.replicas, f.barrier, f.known
	}

	f.replicas, f.barrier = promised.Replicas, promised.Barrier
	f.at, f.revision, f.known = pointer.Object, pointer.Revision, true
	return f.replicas, f.barrier, true
}

// revisionSeen is the fleet revision this node last computed with, for a peer
// asking whether it is behind.
func (f *fleetPolicy) revisionSeen() uint64 {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revision
}

// policy is what the gate that destroys data reads, built from one read so the
// gate cannot be handed an R from one revision and a known from another.
func (f *fleetPolicy) policy(ctx context.Context) Policy {
	replicas, _, known := f.read(ctx)
	return Policy{Replicas: replicas, Known: known}
}
