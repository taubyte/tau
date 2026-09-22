package queue

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// Delivery is one handing-out of one item. Token identifies THIS delivery: an
// Ack or Nack carrying a token the queue no longer holds is refused with
// [ErrStaleDelivery], so a consumer whose claim expired cannot acknowledge work
// that has since been given to somebody else.
type Delivery struct {
	// ID is the item's id, as pushed.
	ID string
	// Data is the payload, as pushed.
	Data []byte
	// Attempt is which delivery this is, counting from one.
	Attempt uint32
	// Deadline is when this claim expires and the item becomes deliverable
	// again.
	Deadline time.Time
	// Token identifies this delivery. It is opaque.
	Token string
}

// Option adjusts a group at construction.
type Option func(*Group) error

// WithClock replaces the source of time. Everything this package decides —
// whether an item is deliverable, whether a claim has expired, when a retry is
// due — is decided against it, so a test can move time without sleeping.
func WithClock(now func() time.Time) Option {
	return func(g *Group) error {
		if now == nil {
			return ErrNoClock
		}
		g.now = now
		return nil
	}
}

// Group is the set of queues over one store, and the composition root for them:
// a queue is opened from a group, never built on its own.
//
// The group is what makes a claim exclusive. Every queue it opens takes the
// SAME lock, so two consumers of one queue exclude each other even when they
// hold two different Queue values for it — which a per-queue lock would not,
// and which nothing stops a caller from doing. It also covers the one write a
// queue makes outside its own keys, the move into the dead-letter queue. Writes
// commit only on the leader, so one process holds every writer, and this is
// that process's half of the mutual exclusion.
//
// The cost is that queues over one store do not write concurrently. They never
// could: their writes are entries in one replicated log, serialized by one
// leader, and the lock is held across a store call precisely so that the read
// it decided on cannot go stale before the write lands.
type Group struct {
	store Store
	now   func() time.Time

	mu sync.Mutex
}

// NewGroup builds a group over store. It reads nothing and writes nothing.
func NewGroup(store Store, opts ...Option) (*Group, error) {
	if store == nil {
		return nil, ErrNoStore
	}
	g := &Group{store: store, now: time.Now}
	for _, opt := range opts {
		if err := opt(g); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// Open returns the queue cfg describes. Opening the same name twice is
// harmless: the two values share the group's lock, so they are one writer.
func (g *Group) Open(cfg Config) (*Queue, error) {
	effective, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	return &Queue{
		group: g,
		cfg:   effective,
		keys:  newKeyspace(effective.Name),
		dead:  newKeyspace(effective.DeadLetter),
	}, nil
}

// Queue is a replicated work queue. See the package documentation for the state
// machine.
//
// A Queue holds no state of its own. Everything is in the store, so a process
// that dies mid-claim leaves the item claimed and the claim expires.
type Queue struct {
	group *Group
	cfg   Config
	keys  keyspace
	dead  keyspace
}

// Config returns the configuration in force, with defaults filled in. Its
// DeadLetter field names the queue exhausted items move to; open that name on
// the same group to drain it.
func (q *Queue) Config() Config { return q.cfg }

// Item is one thing to push: the pair [Queue.Push] takes, for the form that
// takes several.
type Item struct {
	ID   string
	Data []byte
}

// Push adds an item. An id already in the queue — waiting, claimed, or backing
// off — is a no-op, so a producer that cannot tell whether its last push landed
// may repeat it. An id that was acked is gone, and pushing it again is a new
// item.
func (q *Queue) Push(id string, data []byte) error {
	return q.PushWith([]Item{{ID: id, Data: data}})
}

// PushWith adds items and applies the caller's OWN operations in the same
// replicated entry, so a producer gets both or neither.
//
// IT EXISTS BECAUSE A PRODUCER'S RECORD OF A PUSH MUST NOT OUTLIVE THE PUSH. A
// clock claims a period and then pushes the work that period owes; as two
// entries, a leader that dies between them has spent the period and produced
// nothing, and no later turn can tell — the claim it left behind says the work
// was already done. As one entry there is no between. That is the caller shape
// this is for and the only one: a key OUTSIDE this queue whose whole meaning is
// "the push happened".
//
// THE OPERATIONS MAY NOT TOUCH THIS QUEUE OR ITS DEAD LETTER, refused with
// [ErrReservedKey]. Every state transition in this package is one Batch and
// that is what makes an item impossible to lose between two places; a caller
// writing into the middle of those keys would be a second author of them with
// none of the rules.
//
// Everything else is [Queue.Push]'s: an id this queue already holds is skipped
// rather than refused, and no items at all still commits the operations.
func (q *Queue) PushWith(items []Item, also ...raft.BatchOp) error {
	for _, item := range items {
		if item.ID == "" {
			return ErrEmptyID
		}
	}
	for _, op := range also {
		key, err := touched(op)
		if err != nil {
			return err
		}
		if q.owns(key) {
			return fmt.Errorf("%w: %q", ErrReservedKey, key)
		}
	}

	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return err
	}

	ops := append([]raft.BatchOp(nil), also...)
	at := addClamped(q.group.now(), q.cfg.Delay)
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		// AN ID TWICE IN ONE CALL IS ONE ITEM, for the same reason an id
		// already in the queue is one. The index read below is of COMMITTED
		// state, which nothing in this batch has reached, so without this the
		// same id would land under two ready keys with one index naming
		// whichever the apply wrote last — an item nothing can ever claim.
		if seen[item.ID] {
			continue
		}
		seen[item.ID] = true
		if _, exists := q.group.store.Get(q.keys.index(item.ID)); exists {
			continue
		}

		value, err := record{ID: item.ID, Data: item.Data, DeliverAt: at.UnixNano()}.encode()
		if err != nil {
			return err
		}
		key := q.keys.ready(at, item.ID)
		ops = append(ops, set(key, value), set(q.keys.index(item.ID), []byte(key)))
	}

	if len(ops) == 0 {
		return nil
	}
	return q.group.store.Batch(ops, q.cfg.ApplyTimeout)
}

// owns is whether a key is part of this queue's state machine or its dead
// letter's.
func (q *Queue) owns(key string) bool {
	return strings.HasPrefix(key, q.keys.prefix) || strings.HasPrefix(key, q.dead.prefix)
}

// touched is the key one operation acts on, refusing one that names neither
// side or both — which a caller assembling a raft.BatchOp by hand can write and
// which the store would apply as something nobody meant.
func touched(op raft.BatchOp) (string, error) {
	switch {
	case op.Set != nil && op.Delete == nil:
		return op.Set.Key, nil
	case op.Delete != nil && op.Set == nil:
		return op.Delete.Key, nil
	}
	return "", ErrMalformedOp
}

// Remove withdraws an item nobody has claimed: Push's counterpart, for a
// producer that no longer wants the work done. Ready, delayed and backing off
// are all the same case — the item is deleted and its id is pushable again.
//
// AN ITEM A CONSUMER HOLDS IS REFUSED, with [ErrClaimed]. The claim is this
// package's mutual exclusion, and deleting one would let the same id be pushed
// and claimed a second time while the first consumer is still working on it —
// two consumers of one item, which is the whole thing this queue exists to
// prevent. A claimed item leaves by its holder's Ack or by its deadline, and a
// producer that wants it gone waits for one of those.
//
// [ErrNotFound] for an id the queue does not hold, so a producer that cannot
// tell whether its withdrawal landed may repeat it.
func (q *Queue) Remove(id string) error {
	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return err
	}

	raw, exists := q.group.store.Get(q.keys.index(id))
	if !exists {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	key := string(raw)
	state, err := q.keys.parse(key)
	if err != nil {
		return err
	}
	if state.state == claimedSegment {
		return fmt.Errorf("%w: %q", ErrClaimed, id)
	}

	return q.group.store.Batch([]raft.BatchOp{
		del(key),
		del(q.keys.index(id)),
	}, q.cfg.ApplyTimeout)
}

// Claim hands out the item that has been deliverable longest, and makes it
// invisible until its deadline. It returns [ErrNoItem] when nothing is
// deliverable — the queue may still hold items that are delayed, backing off or
// claimed by somebody else — and [ErrNotLeader] on a follower, which claims
// nothing.
//
// Before it looks, it moves every expired claim on: back to ready after its
// retry delay, or to the dead-letter queue if its attempts are spent. That is
// where the visibility timeout is enforced; there is no sweeper. [Queue.Sweep]
// runs the same pass without claiming.
func (q *Queue) Claim() (Delivery, error) {
	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return Delivery{}, err
	}
	now := q.group.now()
	if err := q.repair(now); err != nil {
		return Delivery{}, err
	}

	keys := q.sorted(q.keys.readyPrefix())
	if len(keys) == 0 {
		return Delivery{}, ErrNoItem
	}

	// Ready keys sort by the time they become deliverable, so the first one is
	// the next item due and nothing after it can be due earlier.
	key := keys[0]
	due, err := q.keys.parse(key)
	if err != nil {
		return Delivery{}, err
	}
	if due.at.After(now) {
		return Delivery{}, ErrNoItem
	}
	rec, err := q.load(key)
	if err != nil {
		return Delivery{}, err
	}

	deadline := addClamped(now, q.cfg.VisibilityTimeout)
	rec.Attempts++
	rec.Deadline = deadline.UnixNano()
	rec.Token = newToken(now)
	value, err := rec.encode()
	if err != nil {
		return Delivery{}, err
	}

	claimed := q.keys.claimed(deadline, rec.ID)
	if err := q.group.store.Batch([]raft.BatchOp{
		del(key),
		set(claimed, value),
		set(q.keys.index(rec.ID), []byte(claimed)),
	}, q.cfg.ApplyTimeout); err != nil {
		return Delivery{}, err
	}

	return Delivery{
		ID:       rec.ID,
		Data:     rec.Data,
		Attempt:  rec.Attempts,
		Deadline: deadline,
		Token:    rec.Token,
	}, nil
}

// Ack removes an item for good. It is the only thing that discards a CLAIMED
// one — everything else moves it — and [Queue.Remove] is the only thing that
// discards an item nobody holds.
//
// It succeeds after the claim's deadline has passed, so long as the item has not
// actually been redelivered — a consumer that ran late has still done the work,
// and refusing it would only cause the job to run twice. Once somebody else has
// claimed the item the token no longer matches and the ack is refused with
// [ErrStaleDelivery].
func (q *Queue) Ack(d Delivery) error {
	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return err
	}
	key, rec, err := q.held(d)
	if err != nil {
		return err
	}
	return q.group.store.Batch([]raft.BatchOp{
		del(key),
		del(q.keys.index(rec.ID)),
	}, q.cfg.ApplyTimeout)
}

// Nack gives an item back now rather than waiting out the visibility timeout. It
// counts as a failed delivery: the item comes back after the retry delay for the
// attempt that just failed, or moves to the dead-letter queue if that attempt was
// its last.
func (q *Queue) Nack(d Delivery) error {
	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return err
	}
	key, rec, err := q.held(d)
	if err != nil {
		return err
	}
	now := q.group.now()
	return q.fail(key, rec, now, now)
}

// Sweep advances every expired claim without claiming anything: it is the pass
// Claim runs first, exposed on its own. A queue nobody is claiming from still
// needs it, because that is when an exhausted item reaches the dead letter.
func (q *Queue) Sweep() error {
	q.group.mu.Lock()
	defer q.group.mu.Unlock()

	if err := q.prepare(); err != nil {
		return err
	}
	return q.repair(q.group.now())
}

// Depth reports how many items are waiting — including ones whose delay or
// backoff has not elapsed — and how many are claimed. It reads local state
// without a barrier, so on a follower it can lag; it is for observability, not
// for decisions.
func (q *Queue) Depth() (ready, claimed int) {
	return len(q.group.store.Keys(q.keys.readyPrefix())), len(q.group.store.Keys(q.keys.claimedPrefix()))
}

// prepare refuses a write on a follower and then makes the local state current.
//
// The leader check is not the authority — the store's own Batch is, and it
// fails the same way — but it stops a follower from scanning for an item it
// could never claim. The barrier is load-bearing: a node that has just become
// leader may hold committed entries it has not applied yet, and without it a
// fresh leader could read a stale ready set and hand out an item its predecessor
// already claimed.
func (q *Queue) prepare() error {
	if !q.group.store.IsLeader() {
		return ErrNotLeader
	}
	return q.group.store.Barrier(q.cfg.ApplyTimeout)
}

// repair moves every claim whose deadline has passed. Claimed keys sort by
// deadline, so it stops at the first one still in the future and does work
// proportional to what expired rather than to the depth of the queue.
func (q *Queue) repair(now time.Time) error {
	for _, key := range q.sorted(q.keys.claimedPrefix()) {
		claim, err := q.keys.parse(key)
		if err != nil {
			return err
		}
		if claim.at.After(now) {
			return nil
		}
		rec, err := q.load(key)
		if err != nil {
			return err
		}
		// fail writes to the ready or dead-letter prefix, never back into the
		// claimed one, so the listing above stays valid as it goes.
		if err := q.fail(key, rec, claim.at, now); err != nil {
			return err
		}
	}
	return nil
}

// fail takes an item off the claimed side after a delivery did not succeed:
// back to ready after the retry delay, or to the dead letter once the attempts
// are spent. failedAt is when the delivery failed — a claim's deadline, or now
// for a nack — so a late sweep does not stretch the backoff.
func (q *Queue) fail(key string, rec record, failedAt, now time.Time) error {
	if rec.Attempts >= q.cfg.MaxAttempts {
		return q.bury(key, rec, now)
	}

	at := addClamped(failedAt, backoff(q.cfg, rec.Attempts))
	rec.DeliverAt = at.UnixNano()
	rec.Deadline = 0
	rec.Token = ""
	value, err := rec.encode()
	if err != nil {
		return err
	}

	ready := q.keys.ready(at, rec.ID)
	return q.group.store.Batch([]raft.BatchOp{
		del(key),
		set(ready, value),
		set(q.keys.index(rec.ID), []byte(ready)),
	}, q.cfg.ApplyTimeout)
}

// bury moves an exhausted item into the dead-letter queue, in the same entry
// that takes it out of this one — so it is never in both and never in neither.
// It lands deliverable, with its attempts reset, because the dead-letter queue
// counts its own deliveries.
//
// This reads and writes another queue's keys. The group's lock covers both, so
// whoever is draining the dead-letter queue is excluded for the whole of it.
func (q *Queue) bury(key string, rec record, now time.Time) error {
	ops := []raft.BatchOp{del(key), del(q.keys.index(rec.ID))}

	// The dead-letter queue deduplicates by id like any other queue, so an id it
	// already holds keeps the payload it arrived with rather than being written
	// twice under two keys — the same identity rule Push applies.
	if _, exists := q.group.store.Get(q.dead.index(rec.ID)); !exists {
		value, err := record{ID: rec.ID, Data: rec.Data, DeliverAt: now.UnixNano()}.encode()
		if err != nil {
			return err
		}
		dead := q.dead.ready(now, rec.ID)
		ops = append(ops, set(dead, value), set(q.dead.index(rec.ID), []byte(dead)))
	}

	return q.group.store.Batch(ops, q.cfg.ApplyTimeout)
}

// held resolves a delivery to the key it is claimed under, refusing one the
// queue no longer recognizes.
func (q *Queue) held(d Delivery) (string, record, error) {
	raw, exists := q.group.store.Get(q.keys.index(d.ID))
	if !exists {
		return "", record{}, fmt.Errorf("%w: %q", ErrNotFound, d.ID)
	}

	key := string(raw)
	state, err := q.keys.parse(key)
	if err != nil {
		return "", record{}, err
	}
	rec, err := q.load(key)
	if err != nil {
		return "", record{}, err
	}
	if state.state != claimedSegment || rec.Token == "" || rec.Token != d.Token {
		return "", record{}, fmt.Errorf("%w: %q", ErrStaleDelivery, d.ID)
	}
	return key, rec, nil
}

func (q *Queue) load(key string) (record, error) {
	raw, exists := q.group.store.Get(key)
	if !exists {
		return record{}, fmt.Errorf("%w: %q is indexed but missing", ErrCorruptRecord, key)
	}
	return decodeRecord(key, raw)
}

func (q *Queue) sorted(prefix string) []string {
	keys := q.group.store.Keys(prefix)
	slices.Sort(keys)
	return keys
}

// newToken names one delivery. It only has to be unique among the deliveries of
// one item, so that a consumer whose claim expired cannot acknowledge the
// delivery that replaced it.
func newToken(now time.Time) string {
	return strconv.FormatInt(now.UnixNano(), 36) + "-" + strconv.FormatUint(rand.Uint64(), 36)
}
