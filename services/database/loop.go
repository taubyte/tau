package database

import (
	"context"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/placement"
)

// loop.go is the thing that calls the decisions.
//
// # It walks what this node HOLDS, not what exists
//
// A fleet's databases are not this node's business; its own copies are. That
// makes the sweep O(held) rather than O(fleet), it needs no shared index of
// who-holds-what, and it distributes naturally — every database is watched by
// the nodes holding it.
//
// It also means nobody watches a database whose holders are all gone. That is
// stated rather than solved: after the last holder is lost, an empty store on
// the desired nodes is indistinguishable from a database nobody ever wrote,
// by digest or by heads. The fleet's promise is the floor, and the case where
// the floor was exceeded is a backup question.
//
// # Two sweeps, and only one of them can delete anything
//
// REPAIR asks the OTHER desired holders whether they have a database this node
// holds and should keep. A peer that says it does not is nudged — and it opens
// only on its own computation, never on this node's word, which is the decline
// asymmetry in reverse. Nothing is deleted, so nothing has to be gated.
//
// RELEASE is the other direction, for a database this node should no longer
// hold, and it is gated on the digest — see maintain.go.
//
// WHY REPAIR EXISTS AT ALL, given "nobody lets go until somebody else has it":
// that argument covers MOVES and not node LOSS. A crashed node's copies are
// simply gone, and opening-on-demand restores the floor only for databases
// somebody happens to ask for. Repair is what notices.

// pending is how many of this node's databases are below their floor as far as
// it can tell: a co-holder that answered it does not hold one, or could not be
// reached at all. It is ONE NUMBER on purpose — the fleet figure is the sum over
// fresh nodes, so it costs O(1) per node per interval, and anybody who wants to
// know WHICH asks this node directly.
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
	byRegion := s.members()

	short := 0
	for _, spec := range s.holdings.OnDisk(ctx) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if s.reconsiderOne(ctx, byRegion, spec, floor, known) {
			short++
		}
	}

	s.pendingMu.Lock()
	s.pendingAt = short
	s.pendingMu.Unlock()

	s.reconsiderLedgers(ctx)
}

// reconsiderLedgers asks the other voters of every raft database this node has
// opened a group for whether they have opened theirs.
//
// IT IS THE ONLY THING THAT RE-CHECKS, and without it a ledger runs forever as
// whatever its first round of asking produced. Membership is established once,
// by a best-effort round on a request that failed: warm fires when the local
// group has no leader, and once a leader exists nothing calls it again. A node
// whose peers were booting when it asked bootstraps a cluster of ONE, leads it,
// answers every write from its own disk -- and the declared floor is three. "A
// write costs a round trip" costs none, and losing that node discards writes
// that were acked as committed.
//
// IT WALKS WHAT THIS NODE OPENED AND NOT WHAT IS ON DISK. OnDisk answers about
// the replicated store, which a ledger does not live in, so the sweep above
// never names one.
//
// WHAT IT COSTS, SAID RATHER THAN HIDDEN: one call per other voter per ledger
// per sweep, which is the same order as the repair sweep's -- it asks every
// co-holder about every database this node holds. The cheaper rule is to ask
// only when the group is short of its floor, and the group's own membership is
// not a thing this service can see: pkg/raft answers it to a Cluster holder,
// and what this service holds is a database.Store in front of one.
func (s *Service) reconsiderLedgers(ctx context.Context) {
	for _, at := range s.ledgers() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		declaration, err := s.declared(ctx, at)
		if err != nil {
			// A LEDGER THIS NODE CANNOT RESOLVE IS NOT ONE IT MAY LEAVE. A
			// transport error is not a fact about the declaration, and leaving
			// a group is a membership change rather than something a failed
			// lookup decides.
			continue
		}

		// THE OTHERS AND NOT "THE OTHERS IF THIS NODE IS STILL ONE OF THEM".
		// The group has this node in it either way, and the voters placement
		// names today are who it has to converge with.
		others, _ := s.desired(ctx, at, declaration)
		s.warm(ctx, at, others)
	}
}

// reconsiderOne decides about one copy, and reports whether it is below its
// floor as far as this node can tell.
func (s *Service) reconsiderOne(ctx context.Context, byRegion map[string][]placement.Member, spec database.Spec, floor int, known bool) (short bool) {
	// THE ADDRESS ROUND-TRIPS THROUGH THE SPEC. Name is what storeName wrote,
	// so reading it back as the id reproduces the same key and the same prefix.
	at := address{project: spec.Project, environment: spec.Environment, id: spec.Name}
	key := at.key()

	// ON DISK IS THE AUTHORITY ON WHAT THIS NODE HOLDS, so the sweep records it
	// before deciding anything. Without this a restarted node — which holds
	// everything it held and has served none of it — answers keeps=false about
	// every copy it has, which both freezes every move in the fleet that needs
	// one of them and leaves this node unable to begin releasing its own.
	s.own.hold(key)

	declaration, err := s.declared(ctx, at)
	if err != nil {
		// A COPY THIS NODE CANNOT RESOLVE IS NOT A COPY IT MAY DELETE. Bytes
		// nobody declares are reclaim's business and reclaim is gated on a
		// revision rather than on a failed lookup; a transport error is not a
		// fact about the declaration at all.
		return false
	}

	// A RAFT DATABASE'S COPIES ARE ITS GROUP'S MEMBERSHIP, and this half of the
	// sweep has nothing it may do about one. Its floor is met by voters rather
	// than by converged digests, and letting go of it is RemoveServer rather
	// than a local drop — so a sweep that treated it like a crdt copy would
	// compare a digest nobody keeps and then delete bytes the group still
	// counts on. The rows on disk here are a database that USED to be crdt;
	// reclaiming those is reclaim's business, which is gated on a revision
	// rather than on this. What DOES watch a ledger is reconsiderLedgers.
	if declaration.spec.Consensus == database.Raft {
		return false
	}

	desired, keep := moved(at, byRegion, declaration.regions, floor, s.peer, s.region)
	if keep {
		// THIS NODE IS ONE OF THE HOLDERS, so it checks the others.
		return s.repair(ctx, at, byRegion, declaration.regions, floor)
	}

	// ADDITIVE-ONLY UNTIL THE FLOOR IS KNOWN. A node that has never read what
	// the cloud declared does not know how many copies a database is supposed
	// to have, and the whole of release is a comparison against that number.
	// It keeps serving and keeps what it holds.
	if !known {
		return false
	}

	// ONE RELEASE AT A TIME PER DATABASE, and from here until it resolves this
	// node answers keeps=false — so a peer asking cannot count this copy as a
	// reason to let go of its own.
	if !s.own.begin(key) {
		return false
	}

	if !s.mayGo(ctx, at, desired, floor) {
		s.own.stay(key)
		return false
	}

	// THE ANSWER IS ABOUT THE COPY AS IT WAS WHEN THE ASKING STARTED, and every
	// RTT of that asking is a window this node was still accepting rows in. So
	// writes are shut off here, before anything is deleted — and a copy written
	// into meanwhile is asked about AGAIN rather than deleted on an answer that
	// is no longer about it. The second round cannot be overtaken in turn,
	// because nothing can be written into a sealed copy.
	sealed, clean := s.own.seal(key)
	if !sealed {
		// A WRITE IS HALF DONE. It is already past the fence and would land
		// after the digest the replacements answered, so this node keeps the
		// copy and asks again next sweep.
		s.own.stay(key)
		return false
	}
	if !clean && !s.mayGo(ctx, at, desired, floor) {
		s.own.stay(key)
		return false
	}

	// THE RESIDENT ENGINE IS A HANDLE ON THE STORE THIS IS ABOUT TO DESTROY,
	// and destroying closes it. Dropped before, so nothing is served from a
	// store being deleted, and again after, because a read in between reopens
	// it — a table that kept it would answer not-found for rows the fleet still
	// holds, and put this store's own bookkeeping back under the prefix that
	// was just removed.
	s.engines.forget(key)
	err = s.holdings.Destroy(ctx, spec)
	s.engines.forget(key)
	if err != nil {
		// THE BYTES ARE STILL HERE, so this node still holds the database and
		// says so. Recording it gone would make it answer held=false about data
		// it has, which is how a fleet loses track of a copy that exists.
		s.own.stay(key)
		return false
	}
	s.own.gone(key)
	return false
}

// repair asks this database's OTHER holders whether they have it, and reports
// whether the floor is met as far as this node can tell.
//
// A PEER THAT SAYS NO IS NUDGED AND NEVER TOLD. Hold opens the database on the
// answering node only if that node computes itself desired — its own placement
// answer, from its own inputs. Acquiring on a stranger's word is the decline
// asymmetry running backwards, and it is how a node that placement never chose
// becomes a holder.
//
// UNREACHABLE COUNTS AS SHORT, because it is: a copy this node cannot confirm
// is a copy it cannot count on. This number is advisory, so erring toward
// "somebody look at this" is the right direction — unlike release, where the
// same uncertainty must mean "change nothing".
func (s *Service) repair(ctx context.Context, at address, byRegion map[string][]placement.Member, regions []string, floor int) (short bool) {
	for _, region := range placementRegions(regions) {
		held := 1 // this node
		others := 0
		for _, id := range placement.Desired(resourceOf(at), membersIn(byRegion, region), floor) {
			if id == s.peer {
				continue
			}
			others++
			answer, err := s.nudge(ctx, id, at)
			if err == nil && answer.held {
				held++
			}
		}

		want := floor
		if others+1 < want {
			// The region is smaller than the floor, so the floor is the region
			// — the same bar release applies, and for the same reason.
			want = others + 1
		}
		if held < want {
			short = true
		}
	}
	return short
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
func (s *Service) mayGo(ctx context.Context, at address, desired map[string][]string, floor int) bool {
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

// ask is one peer's answer about one database.
//
// DIALLED PER ASK AND NOT KEPT. A sweep asks a handful of peers about a handful
// of databases every half minute; a pool would be a second thing to expire, and
// a stale connection to a node that has gone is exactly the case where the
// answer must be an error rather than a cached yes.
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
