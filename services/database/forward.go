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

// forward.go is who answers for a raft database, which is the one thing its
// consensus changes about this service.
//
// # A group has one writer and this fleet has many doors
//
// `consensus: raft` promises that a write costs a round trip and that nothing
// is silently discarded. Keeping it means one holder at a time: the leader
// orders the writes and, behind a barrier, is the only node whose applied state
// is everything committed. Every other node in the fleet is still a door — the
// gateway places an invocation wherever it likes — so a call lands on the
// leader by accident and on somebody else the rest of the time.
//
// A crdt database has no such problem, which is why none of this is on that
// path: its copies converge, so whichever node holds one answers from its own.
//
// # The store refuses and this service forwards
//
// node/queue.go's rule is kept exactly: a store that dialled would be a second
// write path with its own failure modes, reaching back up through the service
// that composed it. What is different is that this service HAS somewhere to
// send the call — it already dials peers for the repair sweep — so the store's
// refusal carries the leader's name and the call is re-issued once.
//
// # It is bounded by hops and not by trust
//
// Two nodes whose membership views differ can each believe the other should
// answer. Counting hops is what makes that cost a retryable refusal instead of
// a loop: a forwarded call carries how far it has come, and a call that has
// come far enough is served here or refused here. Nothing decides that from the
// caller's claim about who it is — only from a number that can never go down.
//
// # Nobody opens a group because somebody asked
//
// Opening a raft group makes this node a VOTER in it, which is a heavier
// version of the act Holdings.Held refuses for a crdt copy: a node that
// answered a question by opening would become a holder because it was asked.
// So a node opens only what its OWN placement answer says it should hold, and a
// node that should not forwards instead.
//
// What still has to happen is that the other voters open theirs, or a group of
// one is all there ever is. warm is that: when the group this node opened has
// no leader, it asks the other desired holders about the database, and each of
// them opens on its own answer. Asking is the nudge; the answer is theirs.

// maxHops is how many nodes a call may be sent through. Two is the shape the
// fleet actually has: a node that is not a voter knows who the voters are but
// not which of them leads, and a voter knows the leader — so origin, voter,
// leader, and no further. A third hop would be two nodes disagreeing, which is
// what retrying is for.
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
func (s *Service) voters(ctx context.Context, at address) []string {
	members := s.fleet()
	if len(members) == 0 {
		// A NODE THAT CANNOT SEE A FLEET IS THE WHOLE FLEET as far as it knows,
		// and saying "somebody else holds this" would refuse a database nobody
		// else can answer for. It keeps its own group.
		//
		// IT IS THE ANSWER THAT IS EMPTY AND NOT THE FUNCTION THAT IS ABSENT.
		// Every composition hands this service a closure; a node whose stats
		// have seen nobody yet gets a closure that returns nothing, which is
		// the same fact and was the arm nobody reached.
		return nil
	}

	floor, known := s.floor.read(ctx)
	if !known {
		// A NODE THAT HAS NEVER LEARNED THE FLOOR IS NOT A NODE WHOSE FLOOR IS
		// ZERO. Placement over a target of zero desires nobody, and a group
		// nobody is desired for is one EVERY node opens for itself — which is
		// the opposite of the conservative answer. The default is what every
		// node reading a definitive absence also reaches, so nodes that cannot
		// see the document still agree with each other.
		floor = DefaultReplication
	}

	return holders(at, members, floor)
}

// fleet is this node's membership view, or nothing when it has no way to have
// one.
func (s *Service) fleet() []placement.Member {
	if s.members == nil {
		return nil
	}
	return s.members()
}

// desired splits this database's voters into "this node" and "the others".
//
// ONE PREDICATE FOR ROUTING AND FOR ANSWERING. route decides where a call goes
// and voting decides what this node says about itself, and a node that opened a
// group it then told its peers it did not hold would be a voter the rest of the
// fleet cannot count. An empty voter list is the same fact in both: nobody is
// placed, so this node is the whole fleet.
func (s *Service) desired(ctx context.Context, at address, d declared) (others []string, mine bool) {
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

// route is a raft database as this node can reach it: its own group when
// placement says this node is a voter, and the node that should answer when it
// says otherwise.
func (s *Service) route(ctx context.Context, at address, d declared) (database.KVConn, error) {
	others, mine := s.desired(ctx, at, d)

	// A GROUP THIS NODE HAS ALREADY OPENED IS ANSWERED FROM, whatever placement
	// says today. Placement decides what to OPEN; raft decides who may answer,
	// and it decides it from the group's own configuration -- which is add-only
	// here, because nothing reconciles it. A node that dropped out of the
	// desired set while still leading the group would otherwise refuse the one
	// group it leads, forward to a voter that refuses back, and every read and
	// every write of that ledger would fail until somebody restarted something.
	// Placement is recomputed per call and raft leadership is sticky, so that
	// disagreement is permanent and retrying never converges.
	if !mine {
		mine = s.opened(at.key())
	}

	conn := &routed{service: s, at: at, others: others, hops: hopsOf(ctx)}
	if !mine {
		return conn, nil
	}

	// OPENED BEFORE ANYTHING IS ASKED OF IT, because opening is what starts the
	// group forming and forming is a background job — so the first call is the
	// one that begins the wait rather than the one that serves it.
	local, err := s.here(ctx, at, d)
	if err != nil {
		return nil, err
	}
	s.opening(at)
	conn.local = local
	return conn, nil
}

// opening records that this node has a group for one raft database, and opened
// and ledgers read it back.
//
// A GROUP OUTLIVES THE ANSWER THAT OPENED IT, which is what this exists to
// remember. Opening makes this node a voter; the resident table may evict the
// engine in front of it and placement may rank somebody else tomorrow, and
// neither of those removes a voter from a raft configuration. So the set is
// what this node has actually become part of, and it is what the sweep keeps
// asking about and what route answers from.
func (s *Service) opening(at address) {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	if s.ledgersOpen == nil {
		s.ledgersOpen = map[string]address{}
	}
	s.ledgersOpen[at.key()] = at
}

// opened is whether this node is already a voter in a database's group.
func (s *Service) opened(key string) bool {
	s.ledgerMu.Lock()
	defer s.ledgerMu.Unlock()
	_, open := s.ledgersOpen[key]
	return open
}

// ledgers is every raft database this node has opened a group for.
func (s *Service) ledgers() []address {
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
	service *Service
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

// run puts one operation where it can be answered.
//
// THE LOCAL GROUP FIRST, ALWAYS, when there is one: a node that leads answers
// without a hop, which is the whole point of placing a database rather than
// proxying it.
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
		// NOBODY LEADS THIS GROUP, which for a group that has not formed means
		// its other voters have not opened theirs. Asking them is what makes
		// them, and this call is refused so the caller retries — a write held
		// here and applied once a leader appeared would be accepted before
		// anything ordered it, which is the promise this consensus exists to
		// keep.
		r.service.warm(ctx, r.at, r.others)
	}
	return err
}

// anywhere sends the call to a voter: the first placement ranked, and the next
// one only when that node cannot be reached at all.
//
// THE FIRST ONE, SO TWO HOPS STAY TWO HOPS. A node outside the group cannot
// know which voter leads, and the voter it reaches does — so one address is
// enough and walking the list per call would be two hops that could have been
// one.
//
// A REACH FAILURE IS NOT AN ANSWER, which is why the walk exists at all. A
// voter that has gone would otherwise take every ledger call made through every
// node outside the group with it, for as long as it takes membership to notice
// — and membership deliberately takes minutes, because moving data on a blip is
// worse. A voter that ANSWERS ends the walk even when the answer is a refusal:
// that is the group talking.
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

// Close releases nothing.
//
// THE RESIDENT TABLE OWNS THE CONNECTION UNDERNEATH, and closing it here would
// close a database another caller is holding — this is a route to one, built
// per call, and the table decides how long the thing it routes to lives.
func (r *routed) Close() error { return nil }

// warm asks this database's other desired voters about it, which is what makes
// each of them open its own group.
//
// ONE ROUND AT A TIME AND NEVER ON THE CALLER'S PATH. A group forms in tens of
// seconds and every request in that window would otherwise start its own round
// of asking; what the caller gets instead is the refusal, immediately, and a
// retry that finds a leader.
//
// THE ANSWER IS DISCARDED BECAUSE THE ASKING IS THE POINT. A peer opens on its
// own placement answer or not at all — acquiring on a stranger's word is the
// decline asymmetry running backwards — so what it says about itself changes
// nothing here.
func (s *Service) warm(ctx context.Context, at address, peers []string) {
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

// voting is what this node answers about a raft database, and the one place it
// may become one of its voters.
//
// IT OPENS ON THIS NODE'S OWN ANSWER AND NEVER ON THE ASKER'S. A peer asking
// "have you got this" is why this runs, but what decides is placement computed
// here, from this node's own membership view and the floor the cloud published.
// A node that opened because it was asked would be a voter in a group nobody
// placed it in, which is how a fleet ends up with a quorum it did not choose.
func (s *Service) voting(ctx context.Context, at address, d declared) *databasev2.Holding {
	_, mine := s.desired(ctx, at, d)
	if !mine && !s.opened(at.key()) {
		return &databasev2.Holding{}
	}
	if _, err := s.here(ctx, at, d); err != nil {
		return &databasev2.Holding{}
	}
	s.opening(at)

	// HELD AND KEEPS ARE BOTH TRUE FOR A VOTER, and they are not the question
	// they are for a crdt copy: leaving a raft group is a membership change and
	// not a local drop, so nothing in the maintenance sweep ever asks this in
	// order to delete anything.
	return &databasev2.Holding{Held: true, Keeps: true}
}
