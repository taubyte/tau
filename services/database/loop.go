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
// # One direction, deliberately
//
// The sweep RELEASES and never acquires. Acquiring is opening, which happens
// when something asks, and the gate below is what makes that safe: nobody lets
// go until somebody else has it, so a database is never held by nobody while it
// waits to be asked for.

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
	for _, spec := range s.holdings.OnDisk(ctx) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.reconsiderOne(ctx, byRegion, spec, floor, known)
	}
}

// reconsiderOne decides about one copy, and lets go of it if it may.
func (s *Service) reconsiderOne(ctx context.Context, byRegion map[string][]placement.Member, spec database.Spec, floor int, known bool) {
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
		return
	}

	desired, keep := moved(at, byRegion, declaration.regions, floor, s.peer, s.region)
	if keep {
		return
	}

	// ADDITIVE-ONLY UNTIL THE FLOOR IS KNOWN. A node that has never read what
	// the cloud declared does not know how many copies a database is supposed
	// to have, and the whole of release is a comparison against that number.
	// It keeps serving and keeps what it holds.
	if !known {
		return
	}

	// ONE RELEASE AT A TIME PER DATABASE, and from here until it resolves this
	// node answers keeps=false — so a peer asking cannot count this copy as a
	// reason to let go of its own.
	if !s.own.begin(key) {
		return
	}

	if !s.mayGo(ctx, at, desired, floor) {
		s.own.stay(key)
		return
	}
	if err := s.holdings.Destroy(ctx, spec); err != nil {
		// THE BYTES ARE STILL HERE, so this node still holds the database and
		// says so. Recording it gone would make it answer held=false about data
		// it has, which is how a fleet loses track of a copy that exists.
		s.own.stay(key)
		return
	}
	s.own.gone(key)
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
