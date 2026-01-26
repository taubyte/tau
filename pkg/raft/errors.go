package raft

import "errors"

var (
	// ErrNotLeader is returned when a write is attempted on a non-leader
	ErrNotLeader = errors.New("not leader")

	// ErrNoLeader is returned when no leader is known
	ErrNoLeader = errors.New("no leader")

	// ErrTimeout is returned when an operation times out
	ErrTimeout = errors.New("operation timeout")

	// ErrShutdown is returned when the cluster is shutting down
	ErrShutdown = errors.New("cluster shutdown")

	// ErrKeyNotFound is returned when a key doesn't exist
	ErrKeyNotFound = errors.New("key not found")

	// ErrInvalidCommand is returned for malformed commands
	ErrInvalidCommand = errors.New("invalid command")

	// ErrAlreadyClosed is returned when operating on a closed cluster
	ErrAlreadyClosed = errors.New("cluster already closed")

	// ErrInvalidNamespace is returned for invalid namespace format
	ErrInvalidNamespace = errors.New("invalid namespace")
)
