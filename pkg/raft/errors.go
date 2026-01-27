package raft

import "errors"

var (
	// ErrNotLeader is returned when a write is attempted on a non-leader
	ErrNotLeader = errors.New("not leader")

	// ErrNoLeader is returned when no leader is known
	ErrNoLeader = errors.New("no leader")

	// ErrShutdown is returned when the cluster is shutting down
	ErrShutdown = errors.New("cluster shutdown")

	// ErrInvalidCommand is returned for malformed commands
	ErrInvalidCommand = errors.New("invalid command")

	// ErrAlreadyClosed is returned when operating on a closed cluster
	ErrAlreadyClosed = errors.New("cluster already closed")

	// ErrInvalidNamespace is returned for invalid namespace format
	ErrInvalidNamespace = errors.New("invalid namespace")

	// ErrInvalidBarrier is returned when barrier is zero or exceeds maximum
	ErrInvalidBarrier = errors.New("invalid barrier: must be > 0 and <= MaxGetHandlerBarrierTimeout")

	// ErrInvalidTimeout is returned when timeout is zero or exceeds maximum
	ErrInvalidTimeout = errors.New("invalid timeout: must be > 0 and <= MaxApplyTimeout")
)
