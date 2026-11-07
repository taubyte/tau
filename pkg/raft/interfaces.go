package raft

import (
	"context"
	"io"
	"time"

	"github.com/hashicorp/raft"
	"github.com/libp2p/go-libp2p/core/peer"
)

// Groups opens consensus groups on a node: each namespace is one group, and its
// members are whichever nodes open it, found by discovery.
type Groups interface {
	Group(namespace string) (Cluster, error)
}

type Cluster interface {
	Close() error

	Namespace() string

	// Set returns ErrNotLeader on a member that does not lead.
	Set(key string, value []byte, timeout time.Duration) error

	// Get reads local committed state, stale on a follower unless after a Barrier.
	// false is both an absent key and a failed read: a caller acting on absence
	// uses TryGet. A double embedding MockCluster that overrides Get must override
	// TryGet too, or the override is never consulted.
	Get(key string) ([]byte, bool)

	// TryGet retrieves a value by key and reports a failed read as an error
	// rather than as an absent key. A caller that acts on absence — by
	// allocating from zero, by treating a key as free — MUST use this one.
	TryGet(key string) ([]byte, bool, error)

	// Delete returns ErrNotLeader on a member that does not lead.
	Delete(key string, timeout time.Duration) error

	// Batch applies ops in one log entry; ErrNotLeader on a member that does not
	// lead.
	Batch(ops []BatchOp, timeout time.Duration) error

	Keys(prefix string) []string

	// Apply replicates raw bytes for a custom FSM. timeout must be in
	// (0, MaxApplyTimeout], else ErrInvalidTimeout; ErrNotLeader on a member that
	// does not lead.
	Apply(cmd []byte, timeout time.Duration) (FSMResponse, error)

	// Barrier ensures all preceding operations are committed
	Barrier(timeout time.Duration) error

	IsLeader() bool

	Leader() (peer.ID, error)

	State() raft.RaftState

	WaitForLeader(ctx context.Context) error

	Members() ([]Member, error)

	// AddVoter adds a peer as a voting member (leader only)
	AddVoter(id peer.ID, timeout time.Duration) error

	// RemoveServer removes a node from the cluster (leader only)
	RemoveServer(id peer.ID, timeout time.Duration) error

	TransferLeadership() error
}

type Member struct {
	ID       peer.ID
	Address  raft.ServerAddress
	Suffrage raft.ServerSuffrage
}

type FSM interface {
	raft.FSM
	Get(key string) ([]byte, bool)
	TryGet(key string) ([]byte, bool, error)
	Keys(prefix string) []string
	// ExportState returns full KV state (CRDT entries) for merge/heal
	ExportState() (map[string]CRDTEntry, error)
}

type ClusterInfoResponse struct {
	LeaderID    string
	Term        uint64
	LastIndex   uint64
	MemberCount int
	NodeID      string
}

type FSMResponse struct {
	Error error
	Data  []byte
}

// Exactly one of Set or Delete is non-nil.
type BatchOp struct {
	Set    *SetCommand
	Delete *DeleteCommand
}

type LogStore interface {
	FirstIndex() (uint64, error)
	LastIndex() (uint64, error)
	GetLog(index uint64, log *raft.Log) error
	StoreLog(log *raft.Log) error
	StoreLogs(logs []*raft.Log) error
	DeleteRange(min, max uint64) error
}

type StableStore interface {
	Set(key []byte, val []byte) error
	Get(key []byte) ([]byte, error)
	SetUint64(key []byte, val uint64) error
	GetUint64(key []byte) (uint64, error)
}

type SnapshotStore interface {
	Create(version raft.SnapshotVersion, index, term uint64, configuration raft.Configuration, configurationIndex uint64, trans raft.Transport) (raft.SnapshotSink, error)
	List() ([]*raft.SnapshotMeta, error)
	Open(id string) (*raft.SnapshotMeta, io.ReadCloser, error)
}

// Queue is a replicated FIFO over a Cluster's KV, its keys under a prefix
// derived from its name. Pushing an id already queued is a no-op.
type Queue interface {
	Push(id string, data []byte, timeout time.Duration) error
	Pop(timeout time.Duration) (id string, data []byte, err error)
	Peek() (id string, data []byte, ok bool)
	Len() int
	Close() error
}
