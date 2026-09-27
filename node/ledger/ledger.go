// Package ledger keeps a `consensus: raft` database's entries in the order one
// group committed them.
//
// # Why it is its own package
//
// It is an ADAPTER: it sits between pkg/database, which knows four methods over
// a store and nothing about consensus, and a raft cluster, which knows a log and
// nothing about databases. The composition root is the only place that may NAME
// both — but naming them and implementing the store are different jobs, and
// conflating them is how node/ grew to eight thousand lines with twenty-two of
// these in one namespace.
//
// So the root hands this one thing it can ask: a way to open the group for a
// namespace. Everything this package needs is in its own signature, which is the
// property a type taking *Node does not have: that one can reach anything the
// node has, so nothing about it says what it actually depends on, and it cannot
// be built anywhere except beside the node.
//
// # A ledger is a store, which is the whole of the difference
//
// pkg/database's kv engine is generic over four methods and does the
// per-database prefixing itself, so every other property of a kv database is
// unchanged here by construction rather than by a second implementation agreeing
// with the first. What `consensus: raft` changes is WHERE the four methods go.
//
// # One group per database
//
// The cost of that is real: each namespace is its own libp2p protocol, its own
// heartbeat set, its own log and its own snapshot store. What is NOT affordable
// is the alternative. One group for every ledger on a node would serialize the
// cloud's writes through one leader, put every project's entries in every
// voter's state machine — making placement a decoration, which is the failure
// database.Stores was rewritten to end — and make leaving it RemoveServer over
// everybody's rows at once, with no local drop to reach for. What bounds the
// count is placement: a node opens a group only for the ledgers it is a voter
// for, which is the same bound a topic per database already has.
//
// # A follower refuses rather than answering stale
//
// pkg/raft has no conditional write and Get reads APPLIED state, which lags on a
// follower and lags on a leader that has just won an election. So both halves
// are gated:
//
//   - a write is Set/Delete, which raft refuses off the leader anyway. The
//     refusal is made here first, and carries WHO leads, so the caller is told
//     what happened rather than reading it out of a transport error.
//   - a read takes a Barrier first, because "the value I just wrote" is the one
//     question a ledger's reader always asks, and applied state without a
//     barrier can be older than the write that already returned.
//
// THE BARRIER IS ALSO THE LEADERSHIP PROOF, which is the half that is not about
// lag. A partitioned leader answers IsLeader true until its lease expires and
// its state machine is stale — it would answer "no such key" for an entry that
// is committed. A barrier cannot commit without a quorum, so that node fails
// instead of lying.
//
// WHAT IT COSTS, SAID RATHER THAN HIDDEN: hashicorp/raft's Barrier appends a log
// entry, so a read-heavy ledger grows its log and its snapshot churn at READ
// rate. The cheaper correct rule is VerifyLeader — a quorum heartbeat with no
// log entry — paired with a wait on the applied index; pkg/raft's Cluster
// exposes neither, and hand-rolling a leadership check here would be wrong in
// exactly the window it exists for. That is the follow-up, and it is a change to
// pkg/raft rather than to this package.
//
// # Nothing here forwards
//
// A store that dialled would be a second write path with its own failure modes,
// reaching back up through the service that composed it — node/queue.go's rule.
// What it does instead is name the leader, which is what lets services/database
// send the one operation there.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/database"
	"github.com/taubyte/tau/pkg/raft"
	databaseService "github.com/taubyte/tau/services/database"
)

// Groups opens the raft group a namespace's entries are ordered in.
//
// A FUNCTION AND NOT AN INTERFACE, because it is one question and because it is
// the shape the answer already has: raft.New takes a peer node — a host, a DHT,
// a datastore and a lifetime — none of which a ledger has an opinion about, and
// all of which only the composition root holds.
type Groups func(namespace string) (raft.Cluster, error)

// Domain is where a ledger's group lives. It is NOT the queues' namespace: a
// project's entries and this cloud's own work are different lifetimes, different
// sizes and different blast radii — node/queue.go's argument, and it holds
// harder here because a ledger is somebody's system of record.
const Domain = "/tau/databases/v1"

// Budget is what one write or one barrier may cost.
//
// IT IS A REQUEST'S BUDGET AND NOT AN ELECTION'S. A group that has no leader is
// refused immediately and the caller retries; this bounds the round trip to a
// leader that exists, which is a commit to a quorum.
const Budget = 10 * time.Second

// Namespace is the raft namespace one database's group lives in — the same name
// node/database gives its crdt store, under a different root, so the group and
// the store agree by construction rather than by two expressions that happen to
// match. Spec.Prefix already refuses a separator in any segment, which is what a
// protocol id needs.
func Namespace(spec database.Spec) (string, error) {
	prefix, err := spec.Prefix()
	if err != nil {
		return "", err
	}
	return Domain + "/" + strings.TrimSuffix(prefix, "/"), nil
}

// forming is one database's cluster and how far along opening it is.
type forming struct {
	// done is closed once the cluster has been opened or has failed to open, so
	// a caller can tell "not yet" from "not ever" without waiting.
	done chan struct{}

	cluster raft.Cluster
	err     error
}

// Ledgers is every group this node opened, and the store over each. Construct it
// with New.
type Ledgers struct {
	open   Groups
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
// budget is what one write or one barrier may cost; ZERO IS [Budget], and a
// caller that will not wait that long says so on its context instead.
func New(open Groups, budget time.Duration) (*Ledgers, error) {
	if open == nil {
		return nil, errors.New("ledger: no way to open a group, so no ledger can be kept")
	}
	if budget <= 0 {
		budget = Budget
	}
	return &Ledgers{open: open, budget: budget}, nil
}

// Store is where a `consensus: raft` database keeps its entries.
func (l *Ledgers) Store(spec database.Spec) (database.Store, error) {
	namespace, err := Namespace(spec)
	if err != nil {
		return nil, err
	}

	// FORMING STARTS AT THE OPEN AND NOT AT THE FIRST OPERATION, because it is
	// the longest thing that happens to a ledger and the sooner it starts the
	// sooner the first write lands. It does not block: see group.
	l.group(namespace)

	return &Ledger{
		group:  func() (raft.Cluster, error) { return l.group(namespace) },
		budget: l.budget,
	}, nil
}

// group answers a database's group, starting its formation if nothing has.
//
// IT NEVER BLOCKS. raft.New waits up to thirty seconds for peers to appear so
// that a cloud does not bootstrap a single-node cluster it later has to heal —
// right at boot and wrong on a request path, where node/queue.go measured what
// it costs: the first caller paid it, every other caller queued behind the same
// lock, and the invocation timed out. So the group is opened in the background
// and a call that arrives before it exists is refused by name.
func (l *Ledgers) group(namespace string) (raft.Cluster, error) {
	l.mu.Lock()
	if l.gone {
		l.mu.Unlock()
		return nil, databaseService.ErrGroupClosed
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
			cluster, err := l.open(namespace)

			l.mu.Lock()
			defer l.mu.Unlock()
			g.cluster, g.err = cluster, err
		}()
	}
	l.mu.Unlock()

	select {
	case <-g.done:
	default:
		return nil, databaseService.ErrGroupForming
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case g.err != nil:
		// AN OPEN THAT FAILED IS NOT A LEDGER THAT IS OVER. raft.New spends
		// thirty seconds looking for peers and gives up on whatever the disk,
		// the host or the network was doing at the time; leaving the failure in
		// the map would make one bad minute permanent for that database on this
		// node. The entry goes, so the NEXT call starts a fresh attempt.
		//
		// AND THE REASON TRAVELS RATHER THAN "STILL FORMING", which is what an
		// operator needs: the one that retries forever is the refusal that says
		// nothing about why.
		delete(l.groups, namespace)
		return nil, fmt.Errorf("ledger: opening %s: %w", namespace, g.err)
	case g.cluster == nil:
		// Closed underneath us, which is a node on its way out. It is NOT
		// "still forming": a caller told that retries here for a group that
		// will never form, and what it should do is ask another voter.
		return nil, databaseService.ErrGroupClosed
	}
	return g.cluster, nil
}

// Close releases every group this node opened, once each has finished opening —
// tearing one down mid-bootstrap would leave the goroutine writing into a closed
// store, which is node/queue.go's rule and the same failure.
func (l *Ledgers) Close() error {
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
	group  func() (raft.Cluster, error)
	budget time.Duration
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
	keys := cluster.Keys(prefix)
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
	return databaseService.NotLeading{}
}

// leading is this node's group, if this node may answer for it at all.
//
// A GROUP THAT HAS NOT ELECTED YET IS NOT A GROUP THAT REFUSED, and the
// difference reaches the caller: NotLeading with nobody named is "ask again",
// NotLeading with a peer is "ask that one". Neither waits. The deleted first
// version of this waited one write timeout for an election, because refusing was
// all it could otherwise do; with somewhere to send the call there is something
// better to do than hold the caller.
func (s *Ledger) leading(ctx context.Context) (raft.Cluster, time.Duration, error) {
	budget, err := s.remaining(ctx)
	if err != nil {
		return nil, 0, err
	}

	cluster, err := s.group()
	if err != nil {
		return nil, 0, err
	}
	if cluster.IsLeader() {
		return cluster, budget, nil
	}

	leader, err := cluster.Leader()
	if err != nil {
		return nil, 0, databaseService.NotLeading{}
	}
	return nil, 0, databaseService.NotLeading{Leader: leader.String()}
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
// everything committed.
//
// LEADING IS NOT THE SAME AS KNOWING — the sentence node/cron.go carries too. A
// node that has just won an election holds committed entries it has not applied,
// so a read without this can be older than a write that already returned to its
// caller.
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
		return databaseService.NotLeading{}
	}
	return fmt.Errorf("ledger: writing %s: %w", key, err)
}
