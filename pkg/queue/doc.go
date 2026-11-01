// Package queue is a replicated work queue with claim, visibility timeout,
// acknowledgement, attempt counting and a dead letter (the SQS shape) over any
// replicated store with a single writer and an atomic multi-key write.
//
// pkg/raft's Queue pops before the consumer acts, so a crashed consumer loses the
// work. This implements the semantics v2's schema declares for the queue kind and
// knows nothing about builds, projects or any resource.
//
// # The state machine
//
// An item is in exactly one of three places, named by its id:
//
//	ready   — waiting, with the time it becomes deliverable
//	claimed — handed to a consumer, with the deadline the claim expires at
//	dead    — in the dead-letter queue, which is another Queue over the same store
//
// Every transition is one Batch, one entry in the replicated log, so an item is
// never in neither place. A producer may withdraw an unclaimed item; a claimed one
// it may not, because the claim is the mutual exclusion.
//
//	Push   -> ready(now+Delay)
//	Claim  -> ready -> claimed(now+VisibilityTimeout), Attempts+1
//	Ack    -> claimed -> gone
//	Nack   -> claimed -> ready(now+backoff) or -> dead
//	Remove -> ready -> gone
//	expiry -> claimed -> ready(deadline+backoff) or -> dead
//
// A Queue holds no state beyond its configuration; a process that dies mid-claim
// leaves the item claimed, and the claim expires.
//
// # Enforcement is lazy
//
// Expired claims and exhausted items are moved by the scan at the top of Claim and
// by [Queue.Sweep], the same pass without a claim, never by a background goroutine.
// A sweeper would write, so it would have to follow leadership and could silently
// stop; on the claim path enforcement runs where the invariant could be violated.
// Claimed keys are ordered by deadline, so the scan stops at the first future one.
// [Queue.Sweep] covers an exhausted item when nobody claims again.
//
// # The leader is the only writer
//
// Push, Claim, Ack, Nack, Remove and Sweep check leadership first and return
// [ErrNotLeader] (raft.ErrNotLeader) before doing any work, so a follower consumes
// nothing. [Queue.Depth] works anywhere and may be stale. Every method that reads
// before it writes issues a Barrier, so a fresh leader cannot read a stale ready set
// and hand out an item its predecessor claimed.
//
// # Exclusivity
//
// A claim is exclusive because writes commit only on the leader, that process reads
// behind a barrier, and inside it every transition of every queue over one store
// takes the [Group]'s lock. The lock is the group's because a caller can open one
// name twice; queues are opened only from a group, so a second writer of a name is
// not constructible. It also covers the move into the dead-letter queue. The store
// has no compare-and-swap, so there is nothing else to arbitrate with.
package queue
