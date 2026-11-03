package database

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/placement"
	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
)

// Who answers for a raft database. Raft means one holder at a time: the leader
// orders writes and, behind a barrier, is the only node whose state is everything
// committed. Any node is a door, so the store refuses with the leader's name and
// this service re-issues the call. Forwarding is bounded by a hop count that only
// grows, so disagreeing membership views cost a retryable refusal, not a loop.
// Opening a group makes this node a voter, so it opens only what its own placement
// answer says; warm nudges the other desired voters to open theirs.

// maxHops: origin, voter, leader. A non-voter knows the voters but not the leader; a
// voter knows the leader. A third hop would be views disagreeing, which retrying
// handles.
const maxHops = 2

// hopHeader carries how many nodes a call has already been sent through.
//
// A HEADER AND NOT A FIELD ON THE REQUEST, because it is about the call's route
// and not about the database: adding it to every message in the proto would put
// a transport concern in the vocabulary four times over.
const hopHeader = "Tau-Database-Hops"

type hopsKey struct{}

// hopsIn reads how far a call has come. An unreadable or negative count is read
// as the ceiling rather than as zero: a header this node cannot understand must
// not be the thing that lets a call go round again.
func hopsIn(ctx context.Context, header http.Header) context.Context {
	raw := header.Get(hopHeader)
	if raw == "" {
		return ctx
	}
	hops, err := strconv.Atoi(raw)
	if err != nil || hops < 0 {
		hops = maxHops
	}
	return context.WithValue(ctx, hopsKey{}, hops)
}

// hopsOf is how far the call being served has come. Zero is an origin — a local
// caller, or a peer that sent no count.
func hopsOf(ctx context.Context) int {
	hops, _ := ctx.Value(hopsKey{}).(int)
	return hops
}

// voters is every node that should be in this database's raft group, in the
// order placement ranked them.
func (s *server) voters(ctx context.Context, at address) []string {
	members := s.fleet()
	if len(members) == 0 {
		// A node that sees no fleet is the whole fleet as far as it knows, and keeps its own
		// group.
		return nil
	}

	floor, known := s.floor.read(ctx)
	if !known {
		// A node that has never learned the floor uses the default rather than zero: a
		// target of zero desires nobody, and then every node opens a group for itself.
		floor = DefaultReplication
	}

	return holders(at, members, floor)
}

// fleet is this node's membership view, or nothing when it has no way to have
// one.
func (s *server) fleet() []placement.Member {
	if s.members == nil {
		return nil
	}
	return s.members()
}

// desired splits this database's voters into this node and the others. One predicate
// serves routing and answering, so a node never opens a group it then denies
// holding.
func (s *server) desired(ctx context.Context, at address, d declared) (others []string, mine bool) {
	voters := s.voters(ctx, at)

	others = make([]string, 0, len(voters))
	mine = len(voters) == 0
	for _, id := range voters {
		if id == s.peer {
			mine = true
			continue
		}
		others = append(others, id)
	}
	return others, mine
}

// route is a raft database as this node can reach it: its own group when it is a
// voter, otherwise the node that should answer.
func (s *server) route(ctx context.Context, at address, d declared) (database.KVConn, error) {
	others, mine := s.desired(ctx, at, d)

	// A group this node already opened is answered from, whatever placement says today.
	// Raft membership is add-only here, and leadership is sticky while placement is
	// recomputed per call, so refusing a group this node leads would fail every call
	// until a restart.
	if !mine {
		mine = s.opened(at.key())
	}

	conn := &routed{service: s, at: at, others: others, hops: hopsOf(ctx)}
	if !mine {
		return conn, nil
	}

	// Opened first: opening starts the group forming in the background.
	local, err := s.here(ctx, at, d)
	if err != nil {
		return nil, err
	}
	s.opening(at)
	conn.local = local
	return conn, nil
}

// opening records that this node has a group for one raft database. A group outlives
// the answer that opened it: neither eviction nor a new placement removes a voter,
// so this set is what the sweep asks about and route answers from.
func (s *server) opening(at address) {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgersOpen == nil {
		s.ledgersOpen = map[string]address{}
	}
	s.ledgersOpen[at.key()] = at
}

// opened is whether this node is already a voter in a database's group.
func (s *server) opened(key string) bool {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	_, open := s.ledgersOpen[key]
	return open
}

// ledgers is every raft database this node has opened a group for.
func (s *server) ledgers() []address {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()

	out := make([]address, 0, len(s.ledgersOpen))
	for _, at := range s.ledgersOpen {
		out = append(out, at)
	}
	return out
}

// routed is one raft database: this node's own group when it has one, and the
// node that answers when it does not.
type routed struct {
	service *server
	at      address

	// local is this node's own connection, nil when placement says this node is
	// not one of the group's voters.
	local database.KVConn

	// others is the rest of the desired voters, which is who to ask when this
	// node's own group has no leader — and, when this node is not a voter, who
	// to send the call to.
	others []string

	// hops is how far the call that opened this connection had already come.
	hops int
}

var _ database.KVConn = (*routed)(nil)

// run puts one operation where it can be answered, trying the local group first.
func (r *routed) run(ctx context.Context, op func(database.KVConn) error) error {
	if r.local == nil {
		return r.anywhere(ctx, op)
	}

	err := op(r.local)
	if err == nil {
		return nil
	}

	var elsewhere NotLeading
	switch {
	case errors.As(err, &elsewhere) && elsewhere.Leader != "" && elsewhere.Leader != r.service.peer:
		return r.forward(ctx, elsewhere.Leader, op)

	case errors.Is(err, ErrNotHere):
		// THE GROUP THIS NODE HAD IS GONE -- a node on its way out closes them
		// -- so the call goes to a voter that still has one rather than back to
		// a caller that would retry the same door.
		return r.anywhere(ctx, op)

	case errors.As(err, &elsewhere), errors.Is(err, ErrGroupForming):
		// Nobody leads this group, so its other voters are asked (warm) and the call is
		// refused for retry: a write held until a leader appeared would be accepted before
		// anything ordered it.
		r.service.warm(ctx, r.at, r.others)
	}
	return err
}

// anywhere sends the call to the first voter placement ranked, so two hops stay two,
// and to the next only when a voter cannot be reached. A voter that answers ends the
// walk, even with a refusal.
func (r *routed) anywhere(ctx context.Context, op func(database.KVConn) error) error {
	if len(r.others) == 0 {
		return ErrNotHere
	}

	var err error
	for _, peer := range r.others {
		if err = r.forward(ctx, peer, op); !errors.Is(err, ErrNotHere) {
			return err
		}
	}
	return err
}

// forward re-issues one operation against another node.
func (r *routed) forward(ctx context.Context, peer string, op func(database.KVConn) error) error {
	if peer == "" {
		return ErrNotHere
	}
	if r.hops >= maxHops {
		// A CALL ANOTHER NODE ALREADY SENT ON IS SERVED HERE OR REFUSED HERE.
		// Two nodes whose membership views disagree would otherwise send it to
		// each other for as long as they disagreed.
		return fmt.Errorf("%w: and this call has already come %d nodes", ErrNotLeading, r.hops)
	}

	id, err := libp2ppeer.Decode(peer)
	if err != nil {
		return fmt.Errorf("%w: %s is not a peer: %w", ErrUnreachable, peer, err)
	}
	client, err := NewClient(r.service.host, id)
	if err != nil {
		return fmt.Errorf("%w: reaching %s: %w", ErrUnreachable, peer, err)
	}
	defer client.Close()

	return op(client.kv(r.at, r.hops+1))
}

func (r *routed) Get(ctx context.Context, key string) ([]byte, error) {
	var out []byte
	err := r.run(ctx, func(kv database.KVConn) error {
		value, err := kv.Get(ctx, key)
		out = value
		return err
	})
	return out, err
}

func (r *routed) List(ctx context.Context, prefix string) ([]string, error) {
	var out []string
	err := r.run(ctx, func(kv database.KVConn) error {
		keys, err := kv.List(ctx, prefix)
		out = keys
		return err
	})
	return out, err
}

func (r *routed) Put(ctx context.Context, key string, value []byte) error {
	return r.run(ctx, func(kv database.KVConn) error { return kv.Put(ctx, key, value) })
}

func (r *routed) Delete(ctx context.Context, key string) error {
	return r.run(ctx, func(kv database.KVConn) error { return kv.Delete(ctx, key) })
}

func (r *routed) Watch(ctx context.Context, prefix string) (<-chan string, error) {
	var out <-chan string
	err := r.run(ctx, func(kv database.KVConn) error {
		feed, err := kv.Watch(ctx, prefix)
		out = feed
		return err
	})
	return out, err
}

// Close releases nothing: the resident table owns the connection underneath.
func (r *routed) Close() error { return nil }

// warm asks this database's other desired voters about it, which makes each open its
// own group. One round at a time and off the caller's path. The answer is discarded:
// a peer opens on its own placement answer.
func (s *server) warm(ctx context.Context, at address, peers []string) {
	if len(peers) == 0 {
		return
	}

	key := at.key()
	s.warmMu.Lock()
	if s.warming == nil {
		s.warming = map[string]bool{}
	}
	if s.warming[key] {
		s.warmMu.Unlock()
		return
	}
	s.warming[key] = true
	s.warmMu.Unlock()

	// THE CALLER'S CONTEXT ENDS WITH THE CALLER'S REQUEST, and the asking is
	// about a group that outlives it. It is bounded by its own deadline so a
	// node that has gone cannot hold this round open.
	asking, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)

	go func() {
		defer done()
		defer func() {
			s.warmMu.Lock()
			delete(s.warming, key)
			s.warmMu.Unlock()
		}()

		for _, peer := range peers {
			s.nudge(asking, peer, at)
		}
	}()
}

// voting is what this node answers about a raft database, and the one place it may
// become a voter: on its own placement answer, never the asker's.
func (s *server) voting(ctx context.Context, at address, d declared) *databasev2.Holding {
	_, mine := s.desired(ctx, at, d)
	if !mine && !s.opened(at.key()) {
		return &databasev2.Holding{}
	}
	if _, err := s.here(ctx, at, d); err != nil {
		return &databasev2.Holding{}
	}
	s.opening(at)

	// Leaving a raft group is a membership change, not a local drop, so the sweep never
	// asks this to delete.
	return &databasev2.Holding{Held: true, Keeps: true}
}
