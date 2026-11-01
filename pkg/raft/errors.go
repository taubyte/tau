package raft

import "errors"

var (
	ErrNotLeader = errors.New("not leader")

	ErrNoLeader = errors.New("no leader")

	ErrShutdown = errors.New("cluster shutdown")

	ErrInvalidCommand = errors.New("invalid command")

	// ErrNoFSM is returned by a read on a cluster that has no state machine to
	// read from, which is a cluster that never finished initializing
	ErrNoFSM = errors.New("cluster has no FSM")

	ErrAlreadyClosed = errors.New("cluster already closed")

	ErrInvalidNamespace = errors.New("invalid namespace")

	ErrInvalidBarrier = errors.New("invalid barrier: must be > 0 and <= MaxGetHandlerBarrierTimeout")

	ErrInvalidTimeout = errors.New("invalid timeout: must be > 0 and <= MaxApplyTimeout")

	ErrItemNotFound = errors.New("item not found in queue")

	ErrQueueEmpty = errors.New("queue is empty")

	ErrSplitBrain = errors.New("split-brain detected")

	ErrMergeFailed = errors.New("FSM merge failed")

	ErrHealingInProgress = errors.New("healing already in progress")
)
