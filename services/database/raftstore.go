package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taubyte/tau/pkg/raft"
)

// raftstore.go is `consensus: raft` — the other half of the axis.
//
// # What the author asked for
//
// The vocabulary says it in one line: "raft: one holder at a time, so a write
// costs a round trip and nothing is silently discarded." That is a promise
// about WRITES, and it is the opposite of crdt's: a merge never loses a write
// either, but it resolves two of them into one and the loser is gone with no
// error to anybody. A ledger cannot be written that way.
//
// # Why this is a Store and not an engine
//
// pkg/database's kv engine is generic over a four-method store — get, put,
// delete, list — and does the per-database prefixing itself. So the whole of
// "raft instead of crdt" is which store it is handed, and every other property
// of a kv database is unchanged by construction rather than by a second
// implementation agreeing with the first.
//
// # A follower refuses rather than answering stale
//
// pkg/raft has no conditional write and Get reads APPLIED state, which lags on
// a follower and lags on a leader that has just won an election. So both halves
// are gated:
//
//   - a write is Set/Delete, which raft refuses off the leader anyway. The
//     refusal is made here first so the caller is told what happened rather
//     than reading it out of a transport error.
//   - a read takes a Barrier first, because "the value I just wrote" is the one
//     question a ledger's reader always asks, and applied state without a
//     barrier can be older than the write that returned.
//
// THAT MAKES A RAFT DATABASE ONE THE LEADER SERVES, which is not a limitation
// worked around — it is the residency argument this service was built on,
// arriving in its sharpest form. Where an engine IS decides who can answer, and
// for raft the answer is exactly one node at a time.
type raftStore struct {
	// open is the group, on first use.
	//
	// LAZY BECAUSE A CLUSTER OPENED AT CONSTRUCTION NEVER ELECTS. A node builds
	// its services before it starts them, and raft finds its peers over a swarm
	// that is not up yet — so a group opened then sits with no known peers and
	// aborts its election, and every write after that is told there is no
	// leader. pkg/queue's own group is opened in the background for exactly
	// this reason.
	open    func() (raft.Cluster, error)
	timeout time.Duration

	once    sync.Once
	cluster raft.Cluster
	err     error
}

// group resolves the cluster once. A failure is remembered, because a node
// whose raft will not open is not a node the next request will do better on —
// unlike a database, whose store is somebody else's and may come back.
func (s *raftStore) group() (raft.Cluster, error) {
	s.once.Do(func() { s.cluster, s.err = s.open() })
	return s.cluster, s.err
}

// ErrNotLeading is a raft database reached on a node that is not its leader. It
// is its own arm so a caller can retry elsewhere rather than treat it as a
// store failure.
var ErrNotLeading = errors.New("database: this node does not lead that database's raft group")

func (s *raftStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := s.current(ctx); err != nil {
		return nil, false, err
	}
	cluster, err := s.group()
	if err != nil {
		return nil, false, err
	}
	value, held := cluster.Get(key)
	return value, held, nil
}

func (s *raftStore) Put(ctx context.Context, key string, value []byte) error {
	if err := s.leading(ctx); err != nil {
		return err
	}
	cluster, err := s.group()
	if err != nil {
		return err
	}
	if err := cluster.Set(key, value, s.timeout); err != nil {
		if errors.Is(err, raft.ErrNotLeader) {
			return ErrNotLeading
		}
		return fmt.Errorf("database: writing %s: %w", key, err)
	}
	return nil
}

func (s *raftStore) Delete(ctx context.Context, key string) error {
	if err := s.leading(ctx); err != nil {
		return err
	}
	cluster, err := s.group()
	if err != nil {
		return err
	}
	if err := cluster.Delete(key, s.timeout); err != nil {
		if errors.Is(err, raft.ErrNotLeader) {
			return ErrNotLeading
		}
		return fmt.Errorf("database: deleting %s: %w", key, err)
	}
	return nil
}

func (s *raftStore) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.current(ctx); err != nil {
		return nil, err
	}
	cluster, err := s.group()
	if err != nil {
		return nil, err
	}
	return cluster.Keys(prefix), nil
}

// leading is whether this node may answer for the group at all.
//
// A GROUP THAT HAS NOT ELECTED YET IS NOT A GROUP THAT REFUSED. A raft cluster
// opened moments ago has no leader for as long as an election takes, and a
// database reached in that window would answer "this node does not lead" to a
// caller for whom nothing is wrong. So a miss waits, ONCE, bounded by the same
// timeout a write costs — and then answers with what is true.
//
// WAITING DOES NOT MAKE THIS NODE THE LEADER. If another node won, this one
// still refuses, and the refusal is the caller's cue to ask that one instead.
// Which node that is, is placement's question and not this store's.
func (s *raftStore) leading(ctx context.Context) error {
	cluster, err := s.group()
	if err != nil {
		return err
	}
	if cluster.IsLeader() {
		return nil
	}

	waiting, stop := context.WithTimeout(ctx, s.timeout)
	defer stop()
	if err := cluster.WaitForLeader(waiting); err != nil {
		return fmt.Errorf("%w: no leader answered", ErrNotLeading)
	}
	if !cluster.IsLeader() {
		return ErrNotLeading
	}
	return nil
}

// current is what a read costs: leadership, and a barrier so applied state is
// everything committed.
//
// LEADING IS NOT THE SAME AS KNOWING — the sentence node/cron.go now carries
// too. A node that has just won an election holds committed entries it has not
// applied, so a read without this can be older than a write that already
// returned to its caller.
func (s *raftStore) current(ctx context.Context) error {
	if err := s.leading(ctx); err != nil {
		return err
	}
	cluster, err := s.group()
	if err != nil {
		return err
	}
	if err := cluster.Barrier(s.timeout); err != nil {
		return fmt.Errorf("database: reading behind a barrier: %w", err)
	}
	return nil
}
