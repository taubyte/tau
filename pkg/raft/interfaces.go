package raft

import (
	"context"
	"io"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Cluster represents a Raft consensus cluster
type Cluster interface {
	// Close gracefully shuts down the Raft node
	Close() error

	// Namespace returns the cluster namespace
	Namespace() string

	// --- Built-in Key-Value Operations ---

	// Set stores a key-value pair (replicated via Raft)
	// Returns ErrNotLeader if not leader
	Set(key string, value []byte, timeout time.Duration) error

	// Get retrieves a value by key from local committed state
	// Note: May return stale data on followers (replication lag)
	// For strong consistency, call Barrier() first
	Get(key string) ([]byte, bool)

	// Delete removes a key (replicated via Raft)
	// Returns ErrNotLeader if not leader
	Delete(key string, timeout time.Duration) error

	// Keys returns all keys matching a prefix
	Keys(prefix string) []string

	// --- Low-level Raft Operations ---

	// Apply submits raw bytes to be replicated (for custom FSM)
	// Returns ErrNotLeader if not leader
	// Timeout must be > 0 and <= MaxApplyTimeout, otherwise returns ErrInvalidTimeout
	Apply(cmd []byte, timeout time.Duration) (FSMResponse, error)

	// Barrier ensures all preceding operations are committed
	Barrier(timeout time.Duration) error

	// --- Cluster State ---

	// IsLeader returns true if this node is the current leader
	IsLeader() bool

	// Leader returns the peer ID of the current leader
	Leader() (peer.ID, error)

	// State returns the current Raft state (Follower, Candidate, Leader)
	State() raft.RaftState

	// WaitForLeader blocks until a leader is elected
	WaitForLeader(ctx context.Context) error

	// --- Membership ---

	// Members returns all cluster members
	Members() ([]Member, error)

	// AddVoter adds a peer as a voting member (leader only)
	AddVoter(id peer.ID, timeout time.Duration) error

	// RemoveServer removes a node from the cluster (leader only)
	RemoveServer(id peer.ID, timeout time.Duration) error

	// TransferLeadership transfers leadership to another node
	TransferLeadership() error
}

// Member represents a cluster member
type Member struct {
	ID       peer.ID
	Address  raft.ServerAddress
	Suffrage raft.ServerSuffrage
}

// FSM is the finite state machine interface that embeds raft.FSM and includes KV operations
type FSM interface {
	raft.FSM
	// Get retrieves a value by key from local committed state
	Get(key string) ([]byte, bool)
	// Keys returns all keys matching a prefix
	Keys(prefix string) []string
}

// FSMResponse is the typed response from FSM.Apply
type FSMResponse struct {
	Error error
	Data  []byte
}

// LogStore abstracts Raft log storage
type LogStore interface {
	FirstIndex() (uint64, error)
	LastIndex() (uint64, error)
	GetLog(index uint64, log *raft.Log) error
	StoreLog(log *raft.Log) error
	StoreLogs(logs []*raft.Log) error
	DeleteRange(min, max uint64) error
}

// StableStore abstracts Raft stable storage
type StableStore interface {
	Set(key []byte, val []byte) error
	Get(key []byte) ([]byte, error)
	SetUint64(key []byte, val uint64) error
	GetUint64(key []byte) (uint64, error)
}

// SnapshotStore abstracts snapshot storage
type SnapshotStore interface {
	Create(version raft.SnapshotVersion, index, term uint64, configuration raft.Configuration, configurationIndex uint64, trans raft.Transport) (raft.SnapshotSink, error)
	List() ([]*raft.SnapshotMeta, error)
	Open(id string) (*raft.SnapshotMeta, io.ReadCloser, error)
}
