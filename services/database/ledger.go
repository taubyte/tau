// Package ledger keeps a `consensus: raft` database's entries in the order one group
// committed them: an adapter between pkg/database's four-method store and a raft
// cluster, given only a group opener. A ledger is a store, so the kv engine serves
// it unchanged.
//
// One group per database: each is its own protocol, heartbeats, log and snapshots,
// but one group for every ledger would serialize all writes through one leader and
// make placement meaningless. Placement bounds the count: a node opens only the
// ledgers it votes for.
//
// pkg/raft has no conditional write and Get reads applied state, so writes are
// refused off the leader (naming it), and reads take a Barrier first, which also
// proves leadership: a partitioned leader cannot commit one and fails instead of
// answering stale. A Barrier appends a log entry, so reads grow the log; VerifyLeader
// plus an applied-index wait would be cheaper, but pkg/raft exposes neither.
//
// Nothing forwards: the store names the leader so services/database can send the
// operation there.
package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/raft"
)

// LedgerDomain is where a ledger's group lives, apart from the queues' namespace: a system
// of record has a different lifetime and blast radius.
const LedgerDomain = "/tau/databases/v2"

// LedgerBudget is what one write or one barrier may cost.
//
// IT IS A REQUEST'S BUDGET AND NOT AN ELECTION'S. A group that has no leader is
// refused immediately and the caller retries; this bounds the round trip to a
// leader that exists, which is a commit to a quorum.
const LedgerBudget = 10 * time.Second

// LedgerNamespace is the raft namespace one database's group lives in — the same name
// Name gives its crdt store, under a different root, so the group and
// the store agree by construction rather than by two expressions that happen to
// match. Spec.Prefix already refuses a separator in any segment, which is what a
// protocol id needs.
func LedgerNamespace(spec database.Spec) (string, error) {
	prefix, err := spec.Prefix()
	if err != nil {
		return "", err
	}
	return LedgerDomain + "/" + strings.TrimSuffix(prefix, "/"), nil
}

// forming is one database's cluster and how far along opening it is.
type forming struct {
	// done is closed once the cluster has been opened or has failed to open, so
	// a caller can tell "not yet" from "not ever" without waiting.
	done chan struct{}

	cluster raft.Cluster
	err     error
}

// RaftLedgers is every group this node opened, and the store over each. Construct it
// with New.
type RaftLedgers struct {
	open   raft.Groups
	budget time.Duration

	mu     sync.Mutex
	groups map[string]*forming

	// gone is a node that has closed its groups. IT IS NOT "THE MAP IS EMPTY":
	// Close empties it, and a request already past the resident lookup would
	// otherwise open a fresh group afterwards -- a live voter, with a protocol
	// handler and a heartbeat and a vote in a quorum, that nothing will ever
	// close again.
	gone bool
}

// New wires the table.
//
// open may not be nil: without it there is nowhere to order a ledger's writes,
// and a node composed that way keeps no ledger at all — which is what makes the
// service's refusal by name true rather than a guess.
//
// budget is what one write or one barrier may cost; ZERO IS [LedgerBudget], and a
// caller that will not wait that long says so on its context instead.
func NewRaftLedgers(open raft.Groups, budget time.Duration) (*RaftLedgers, error) {
	if open == nil {
		return nil, errors.New("ledger: no way to open a group, so no ledger can be kept")
	}
	if budget <= 0 {
		budget = LedgerBudget
	}
	return &RaftLedgers{open: open, budget: budget}, nil
}

// Store is where a `consensus: raft` database keeps its entries.
func (l *RaftLedgers) Store(spec database.Spec) (database.Store, error) {
	namespace, err := LedgerNamespace(spec)
	if err != nil {
		return nil, err
	}

	// FORMING STARTS AT THE OPEN AND NOT AT THE FIRST OPERATION, because it is
	// the longest thing that happens to a ledger and the sooner it starts the
	// sooner the first write lands. It does not block: see group.
	l.group(namespace)

	return &Ledger{ledgers: l, namespace: namespace, budget: l.budget}, nil
}

// group answers a database's group, starting its formation if needed. It never
// blocks: opening waits up to thirty seconds for peers, so the group opens in the
// background and an early call is refused by name.
func (l *RaftLedgers) group(namespace string) (raft.Cluster, error) {
	l.mu.Lock()
	if l.gone {
		l.mu.Unlock()
		return nil, ErrGroupClosed
	}
	g, opening := l.groups[namespace]
	if !opening {
		g = &forming{done: make(chan struct{})}
		if l.groups == nil {
			l.groups = map[string]*forming{}
		}
		l.groups[namespace] = g

		go func() {
			defer close(g.done)
			cluster, err := l.open.Group(namespace)

			l.mu.Lock()
			defer l.mu.Unlock()
			g.cluster, g.err = cluster, err
		}()
	}
	l.mu.Unlock()

	select {
	case <-g.done:
	default:
		return nil, ErrGroupForming
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case g.err != nil:
		// A failed open is not a finished ledger: the entry is removed so the next call
		// retries, and the reason is returned rather than "still forming".
		delete(l.groups, namespace)
		return nil, fmt.Errorf("ledger: opening %s: %w", namespace, g.err)
	case g.cluster == nil:
		// Closed underneath us, which is a node on its way out. It is NOT
		// "still forming": a caller told that retries here for a group that
		// will never form, and what it should do is ask another voter.
		return nil, ErrGroupClosed
	}
	return g.cluster, nil
}

// Close releases every group this node opened, once each has finished opening;
// tearing one down mid-bootstrap would write into a closed store.
func (l *RaftLedgers) Close() error {
	l.mu.Lock()
	groups := l.groups
	l.groups = nil
	l.gone = true
	l.mu.Unlock()

	var err error
	for _, g := range groups {
		<-g.done
		if g.cluster != nil {
			err = errors.Join(err, g.cluster.Close())
		}
	}
	return err
}

// Ledger is one database's entries, over one group.
type Ledger struct {
	ledgers   *RaftLedgers
	namespace string
	budget    time.Duration
}

var _ database.Store = (*Ledger)(nil)

func (s *Ledger) Get(ctx context.Context, key string) ([]byte, bool, error) {
	cluster, err := s.current(ctx)
	if err != nil {
		return nil, false, err
	}
	value, held := cluster.Get(key)
	if !held {
		return nil, false, missed(cluster)
	}
	return value, held, nil
}

func (s *Ledger) Put(ctx context.Context, key string, value []byte) error {
	cluster, budget, err := s.leading(ctx)
	if err != nil {
		return err
	}
	if err := cluster.Set(key, value, budget); err != nil {
		return s.wrote(key, err)
	}
	return nil
}

func (s *Ledger) Delete(ctx context.Context, key string) error {
	cluster, budget, err := s.leading(ctx)
	if err != nil {
		return err
	}
	if err := cluster.Delete(key, budget); err != nil {
		return s.wrote(key, err)
	}
	return nil
}

// List names every key under a prefix, with the full stored key — which is what
// database.Store owes and what the kv engine cuts its own prefix off.
//
// THE LEADERSHIP CHECK IS WHAT ERRORS, because Keys CANNOT. A closed cluster and
// a nil state machine both answer an empty slice, so a group that is gone and a
// ledger nobody has written to are the same answer — and "the ledger is empty"
// is a fact a caller acts on. The check is made on both sides of the read,
// because the group can go between them: see missed.
func (s *Ledger) List(ctx context.Context, prefix string) ([]string, error) {
	cluster, err := s.current(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := cluster.Keys(prefix)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		if err := missed(cluster); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// missed is what an empty answer from the state machine means.
//
// A CLOSED CLUSTER AND A NIL STATE MACHINE BOTH ANSWER EMPTY, and the kv engine
// turns that into ErrNotFound — "no such key", which is a fact a ledger's reader
// acts on and the one answer this package must never invent. current() proves
// leadership BEFORE the read, and a node shutting down between the proof and the
// read would answer a miss for an entry that is committed. So the proof is made
// again after it: a node that no longer leads did not answer a miss, it did not
// answer.
func missed(cluster raft.Cluster) error {
	if cluster.IsLeader() {
		return nil
	}
	return NotLeading{}
}

// leading is this node's group, if it may answer for it. A group with no leader yet is
// NotLeading with nobody named ("ask again"); one with a leader names it. Neither
// waits.
func (s *Ledger) leading(ctx context.Context) (raft.Cluster, time.Duration, error) {
	budget, err := s.remaining(ctx)
	if err != nil {
		return nil, 0, err
	}

	cluster, err := s.ledgers.group(s.namespace)
	if err != nil {
		return nil, 0, err
	}
	if cluster.IsLeader() {
		return cluster, budget, nil
	}

	leader, err := cluster.Leader()
	if err != nil {
		return nil, 0, NotLeading{}
	}
	return nil, 0, NotLeading{Leader: leader.String()}
}

// remaining is what one operation may cost: this ledger's ceiling, cut short by
// a caller that will not wait that long.
//
// THE CALLER'S DEADLINE IS THE CALLER'S, which pkg/raft takes as a duration and
// not as a context — so a cancelled caller would otherwise pay the full ten
// seconds for a commit nobody is still waiting for, on a request path where the
// gateway has already given up.
func (s *Ledger) remaining(ctx context.Context) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	deadline, set := ctx.Deadline()
	if !set {
		return s.budget, nil
	}
	left := time.Until(deadline)
	if left <= 0 {
		return 0, context.DeadlineExceeded
	}
	return min(left, s.budget), nil
}

// current is what a read costs: leadership, and a barrier so applied state is
// everything committed. A new leader holds committed entries it has not applied.
func (s *Ledger) current(ctx context.Context) (raft.Cluster, error) {
	cluster, budget, err := s.leading(ctx)
	if err != nil {
		return nil, err
	}
	if err := cluster.Barrier(budget); err != nil {
		return nil, fmt.Errorf("ledger: reading %s behind a barrier: %w", cluster.Namespace(), err)
	}
	return cluster, nil
}

// wrote is what a refused write means.
//
// LEADERSHIP CAN GO BETWEEN THE CHECK AND THE APPLY, and when it does raft says
// so — which is the same sentence leading() makes and reaches the caller as the
// same arm, rather than as a store failure it would give up on.
func (s *Ledger) wrote(key string, err error) error {
	if errors.Is(err, raft.ErrNotLeader) {
		return NotLeading{}
	}
	return fmt.Errorf("ledger: writing %s: %w", key, err)
}
