package database

import (
	"context"
	"errors"
	"sync"

	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/register"
)

// fleet.go is where the replication floor comes from.
//
// # It is one value the fleet shares, and it used to be each node's config
//
// Placement takes four inputs and all four are per-node views. Three of them
// heal: a pin read at an older register revision, a membership view that has
// not caught up, a region reported a moment ago. The floor did not. Two nodes
// configured differently disagreed about who should hold a database forever,
// and `copies()` — what an operator is billed for — depended on which node was
// asked.
//
// So it is published like a project: content-addressed, monotone revision, read
// from the register every node already polls. A node that is behind is behind
// by a revision it can name.
//
// # Absent, unreachable, and never read are three different answers
//
//	NOT PUBLISHED   this cloud has said nothing -> the default, which every
//	                node reaches identically, so they still agree
//	UNREACHABLE     change nothing; keep the last value read
//	NEVER READ      the node may ACQUIRE but may not RELEASE -- reclaim's
//	                "cannot see is not gone", generalised. A node that has
//	                never learned the floor must not delete data on a guess
//	                about it.

// fleetSource is the slice of a register this needs. It is asserted rather than
// required of register.Registry, because a consumer that only resolves projects
// is a legitimate thing to build a service over.
type fleetSource interface {
	ResolveFleet(ctx context.Context) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// fleetFloor reads and remembers the cloud's replication floor.
type fleetFloor struct {
	from fleetSource

	mu       sync.Mutex
	known    bool
	at       register.ObjectID
	revision uint64
	value    int
}

// read answers the floor, and whether this node has ever learned one.
//
// THE SECOND RETURN IS NOT A CONVENIENCE. A node that has never read the floor
// does not know how many copies a database is supposed to have, and the whole
// of release is a comparison against that number. Guessing it is how a node
// deletes the second of two copies believing the floor was one.
func (f *fleetFloor) read(ctx context.Context) (int, bool) {
	if f == nil || f.from == nil {
		// A SERVICE COMPOSED WITHOUT A FLEET SOURCE knows the default and
		// nothing else — which is agreed, because every such node reaches it
		// the same way.
		return DefaultReplication, true
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	pointer, err := f.from.ResolveFleet(ctx)
	switch {
	case errors.Is(err, register.ErrNotPublished):
		// A DEFINITIVE ABSENCE IS STILL AGREEMENT: every node reads the same
		// nothing and reaches the same default.
		f.known, f.value = true, DefaultReplication
		return f.value, true
	case err != nil:
		// Unreachable changes nothing, and says so if nothing was ever read.
		return f.value, f.known
	}

	if f.known && pointer.Object == f.at {
		return f.value, true
	}

	raw, err := f.from.Object(ctx, pointer.Object)
	if err != nil {
		return f.value, f.known
	}
	document, err := fleet.Decode(raw)
	if err != nil {
		// A CORRUPT DECLARATION IS NOT SILENCE. Falling back to the default
		// here would have this node place differently from one that read the
		// document before it broke — the disagreement the pointer exists to
		// end. It keeps what it had, and has nothing if it had nothing.
		return f.value, f.known
	}

	f.value = document.Replication
	if f.value == 0 {
		f.value = DefaultReplication
	}
	f.at, f.revision, f.known = pointer.Object, pointer.Revision, true
	return f.value, true
}

// revisionSeen is the fleet revision this node last computed with, for a peer
// asking whether it is behind.
func (f *fleetFloor) revisionSeen() uint64 {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revision
}
