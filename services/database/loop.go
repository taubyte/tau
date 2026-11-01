package database

import (
	"context"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/placement"
)

// The sweep walks what this node holds, not what exists: O(held), no shared index,
// and every database is watched by its holders. Nobody watches a database whose
// holders are all gone; past the floor that is a backup question.
//
// Repair asks the other desired holders whether they have a database this node keeps;
// a peer that says no is nudged and opens only on its own computation. Nothing is
// deleted. Release, the other direction, is gated on the digest (maintain.go). Repair
// exists because node loss, unlike a move, loses copies nobody asks for.

// pending is how many of this node's databases are below their floor as far as it
// can tell (a co-holder said no or was unreachable). One number, so the fleet total
// is cheap; which ones is asked of the node.
func (s *Service) pending() int {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	return s.pendingAt
}

// FleetRevision is the revision of the cloud document this node last computed
// with. A node behind is behind by a number every other node can see.
func (s *Service) FleetRevision() uint64 { return s.floor.revisionSeen() }

// Pending is how many databases this node holds that are below their floor.
func (s *Service) Pending() int { return s.pending() }

// run reconsiders what this node holds, on an interval, until the context ends.
func (s *Service) run(ctx context.Context) {
	if s.holdings == nil || s.members == nil {
		// A node that cannot see a fleet, or cannot let go of anything, has
		// nothing to reconsider. It still serves.
		return
	}

	t := time.NewTicker(s.sweep)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.reconsider(ctx)
		}
	}
}

// reconsider walks this node's copies once.
func (s *Service) reconsider(ctx context.Context) {
	floor, known := s.floor.read(ctx)
	members := s.members()

	short := 0
	for _, spec := range s.holdings.OnDisk(ctx) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if s.reconsiderOne(ctx, members, spec, floor, known) {
			short++
		}
	}

	s.pendingMu.Lock()
	s.pendingAt = short
	s.pendingMu.Unlock()

	s.reconsiderLedgers(ctx)
}

// reconsiderLedgers asks the other voters of every raft database this node opened a
// group for whether they have opened theirs. warm fires only while a group has no
// leader, so without this a node whose peers were booting would lead a group of one
// forever, acking writes nothing replicated. It walks opened groups, not OnDisk,
// which never names a ledger. Cost: one call per other voter per ledger per sweep.
func (s *Service) reconsiderLedgers(ctx context.Context) {
	for _, at := range s.ledgers() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		declaration, err := s.declared(ctx, at)
		if err != nil {
			// A ledger this node cannot resolve is not one it may leave.
			continue
		}

		// The others whether or not this node is still desired: it is in the group either
		// way.
		others, _ := s.desired(ctx, at, declaration)
		s.warm(ctx, at, others)
	}
}

// reconsiderOne decides about one copy, and reports whether it is below its
// floor as far as this node can tell.
func (s *Service) reconsiderOne(ctx context.Context, members []placement.Member, spec database.Spec, floor int, known bool) (short bool) {
	// THE ADDRESS ROUND-TRIPS THROUGH THE SPEC. Name is what storeName wrote,
	// so reading it back as the id reproduces the same key and the same prefix.
	at := address{project: spec.Project, environment: spec.Environment, id: spec.Name}
	key := at.key()

	// On disk is the authority on what this node holds, recorded before deciding, or a
	// restarted node answers keeps=false about every copy and freezes the fleet's moves.
	s.own.hold(key)

	declaration, err := s.declared(ctx, at)
	if err != nil {
		// A copy this node cannot resolve is not one it may delete.
		return false
	}

	// A raft database's copies are its group's membership: its floor is met by voters
	// and leaving is RemoveServer, so this half of the sweep does nothing about one.
	// reconsiderLedgers watches ledgers.
	if declaration.spec.Consensus == database.Raft {
		return false
	}

	desired, keep := moved(at, members, floor, s.peer)
	if keep {
		// THIS NODE IS ONE OF THE HOLDERS, so it checks the others.
		return s.repair(ctx, at, members, floor)
	}

	// Additive-only until the floor is known.
	if !known {
		return false
	}

	// One release at a time per database; until it resolves this node answers
	// keeps=false.
	if !s.own.begin(key) {
		return false
	}

	if !s.mayGo(ctx, at, desired, floor) {
		s.own.stay(key)
		return false
	}

	// Writes are sealed before anything is deleted; a copy written into during the
	// asking is asked about again, which cannot be overtaken since a sealed copy takes
	// no writes.
	sealed, clean := s.own.seal(key)
	if !sealed {
		// A write is half done and would land after the answered digest, so the copy stays.
		s.own.stay(key)
		return false
	}
	if !clean && !s.mayGo(ctx, at, desired, floor) {
		s.own.stay(key)
		return false
	}

	// The resident engine is a handle on the store about to be destroyed: dropped before,
	// and again after since a read in between reopens it.
	s.engines.forget(key)
	err = s.holdings.Destroy(ctx, spec)
	s.engines.forget(key)
	if err != nil {
		// The bytes are still here, so this node still holds the database.
		s.own.stay(key)
		return false
	}
	s.own.gone(key)
	return false
}

// repair asks this database's other holders whether they have it, and reports whether
// the floor is met as far as this node can tell. A peer saying no opens only on its
// own placement answer. Unreachable counts as short: this number is advisory, unlike
// release.
func (s *Service) repair(ctx context.Context, at address, members []placement.Member, floor int) (short bool) {
	held := 1 // this node
	others := 0
	for _, id := range holders(at, members, floor) {
		if id == s.peer {
			continue
		}
		others++
		answer, err := s.nudge(ctx, id, at)
		if err == nil && answer.held {
			held++
		}
	}

	// A fleet smaller than the floor is its own bar — the same one release
	// applies, and for the same reason.
	return held < min(floor, others+1)
}

// nudge is the cheap ask: has this peer got the database, and does it keep it.
func (s *Service) nudge(ctx context.Context, peer string, at address) (keeper, error) {
	id, err := libp2ppeer.Decode(peer)
	if err != nil {
		return keeper{}, err
	}

	client, err := NewClient(s.host, id)
	if err != nil {
		return keeper{}, err
	}
	defer client.Close()

	return client.Hold(ctx, at)
}

// mayGo is this node's own digest, compared against its replacements'.
func (s *Service) mayGo(ctx context.Context, at address, desired []string, floor int) bool {
	kv, err := s.kv(ctx, at)
	if err != nil {
		return false
	}
	keys, sum, err := digestOf(ctx, kv)
	if err != nil {
		return false
	}
	return release(ctx, at, keys, sum, desired, floor, s.ask)
}

// ask is one peer's answer about one database, dialled per ask so a gone node is an
// error rather than a cached yes.
func (s *Service) ask(ctx context.Context, peer string, at address) (keeper, error) {
	id, err := libp2ppeer.Decode(peer)
	if err != nil {
		return keeper{}, err
	}

	client, err := NewClient(s.host, id)
	if err != nil {
		return keeper{}, err
	}
	defer client.Close()

	return client.Digest(ctx, at)
}
