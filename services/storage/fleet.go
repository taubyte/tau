package storage

import (
	"context"
	"errors"
	"sync"

	"github.com/taubyte/tau/pkg/fleet"
	"github.com/taubyte/tau/pkg/register"
)

// fleet.go is where R and K come from.
//
// # They are the cloud's numbers, and they used to be each node's config
//
// R is the first kind of published number: one every node must compute with
// identically. Placement takes four inputs and three of them heal — a pin read
// at an older register revision, a membership view that has not caught up, a
// region reported a moment ago. R did not. Two nodes configured differently
// computed different desired sets forever, disagreed about which shards each
// should open at all, and billed a tenant's bytes at whichever node was asked.
//
// K is the second kind: one that states what this cloud promises. Two nodes
// with different K do not disagree about a shared quantity — each waits for its
// own count — so the failure is quieter and worse. An operator writes
// Barrier: 1 to bring one node up, adds two more, forgets the first, and that
// node acks writes alone forever. Nothing compares two nodes' barriers, so no
// peer, test or caller can see it. A guarantee that varies per node is not a
// guarantee the cloud can state.
//
// # Absent, unreachable, and never read are three different answers
//
//	NOT PUBLISHED   this cloud has said nothing -> the defaults, which every
//	                node reaches identically, so they still agree
//	UNREACHABLE     change nothing; keep the last values read
//	NEVER READ      see below, and it is not one rule
//
// # NEVER READ IS TWO RULES, BECAUSE R AND K ARE NOT THE SAME KIND OF NUMBER
//
// R's unknown is ADDITIVE-ONLY. A node with no R may serve every read, open
// every shard, import blocks and write reference rows: a copy too many costs
// space. What it may not do is drop a misplaced block, because gate 5 is
// ProvenHolders < R and a node with no R has no right-hand side — guessing 3
// where the cloud says 5 drops bytes two replicas short.
//
// It may still collect a DEAD object, and that asymmetry is deliberate rather
// than an oversight: gates 1-4 never mention R, and a dead object is dead at
// every replication factor. Refusing dead-object collection on an unknown R
// would stop reclamation on every node that lost the register — a disk
// exhaustion bought for no safety at all.
//
// K's unknown is FAIL-CLOSED. A put that cannot learn K cannot say how many
// acknowledgements it waited for, and the only honest answer is to fail the
// write. Picking a number is the same shipped lie reached from a third
// direction.
//
// THE COST IS STATED RATHER THAN DISCOVERED: a cold storage node booting into a
// register outage is READ-ONLY. That is the right direction — a refused write
// is visible and a lied-to write is not — but it is a real availability change,
// and doc.go carries it too.

// fleetSource is the slice of a register this needs. It is asserted rather than
// required of register.Registry, because a consumer that only resolves projects
// is a legitimate thing to build a service over.
type fleetSource interface {
	ResolveFleet(ctx context.Context) (register.Pointer, error)
	Object(ctx context.Context, id register.ObjectID) ([]byte, error)
}

// fleetPolicy reads and remembers what the cloud promises about objects.
//
// IT IS NOT services/database's fleetFloor SHARED. The two differ in exactly
// the thing a shared type would have to state — what known == false authorises
// — and a type whose contract is "it depends who is asking" is worse than the
// seventy lines it saves. Database's unknown floor costs nothing while a node
// keeps serving reads; this one closes the write path.
type fleetPolicy struct {
	from fleetSource

	mu       sync.Mutex
	known    bool
	at       register.ObjectID
	revision uint64
	replicas int
	barrier  int
}

// read answers R and K, and whether this node has ever learned them.
//
// ONE READER FOR BOTH NUMBERS AND NOT TWO. They come from one object at one
// revision, and reading them separately would let a node compute with R from
// revision 7 and K from revision 8 — which is the K > R window Resolved exists
// to close, reopened by the reader.
//
// THE THIRD RETURN IS NOT A CONVENIENCE. A node that has never read them does
// not know how many copies an object is supposed to have, and gate 5 is a
// comparison against that number; nor does it know what a write promised.
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
		// A PAIR THAT CANNOT BE CORRECT IS THE SAME CASE, and it arrives here
		// rather than at startup now that it is the cloud's to declare: a
		// barrier above the replica target would fail every write, so the
		// document is refused exactly like one that will not parse.
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
