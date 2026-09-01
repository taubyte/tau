// Package queue is a replicated work queue with claim, visibility timeout,
// acknowledgement, attempt counting and a dead letter — the SQS shape — over any
// replicated store that has a single writer and an atomic multi-key write.
//
// # Why this exists
//
// pkg/raft's Queue is Push/Pop, and its own contract says "Pop removes and
// returns". An item is gone from the log before the consumer has done anything
// with it, so a consumer that crashes takes the work with it, and a fan-out that
// pops from N peers strands N-1 items. This package is the replacement, built to
// the semantics v2's schema already declares for the queue kind: visibility
// timeout, delay, max attempts, retry interval/factor/ceiling and a dead-letter
// queue. pkg/raft's Queue is left exactly as it is — v1 runs on it.
//
// It is not tau-specific: nothing in here knows about builds, projects or any
// other resource. It is not in pkg/raft because pkg/raft is v1's.
//
// It is the SEMANTICS, not the resource. A user-facing queue and a
// platform-internal one are both built on it; tau's own build queue is internal,
// deliberately, because the build service is what builds the platform a resource
// would live on and a resource there would be a bootstrap cycle.
//
// # The state machine
//
// An item is in exactly one of three places, and it is named by its id:
//
//	ready   — waiting, with the time it becomes deliverable
//	claimed — handed to a consumer, with the deadline the claim expires at
//	dead    — in the dead-letter queue, which is another Queue over the same store
//
// Every transition is ONE Batch, which is one entry in the replicated log, so
// there is no instant at which an item is in neither place. That is the whole of
// the durability argument: nothing leaves except by an ack, and every move is
// atomic.
//
//	Push   -> ready(now+Delay)
//	Claim  -> ready -> claimed(now+VisibilityTimeout), Attempts+1
//	Ack    -> claimed -> gone
//	Nack   -> claimed -> ready(now+backoff) or -> dead
//	expiry -> claimed -> ready(deadline+backoff) or -> dead
//
// The Queue value itself holds no state beyond its configuration. A process that
// dies mid-claim leaves the item claimed, and the claim expires.
//
// # Enforcement is lazy, not a sweeper
//
// A claim that expires becomes deliverable again, and an item that has used up
// its attempts moves to the dead letter. Both transitions are performed by the
// scan at the top of Claim — and by [Queue.Sweep], which is the same pass with
// no claim at the end — never by a background goroutine.
//
// The reason is not cost, it is lifecycle. A sweeper writes, so it may only run
// on the leader; it would have to watch leadership, start, stop, and be shut down
// with the queue, and a sweeper that silently stopped would look exactly like a
// queue that works until the first crash. Doing it on the claim path means the
// enforcement runs on the only path that can violate the invariant, and there is
// no second lifecycle to get wrong. What it costs is that Claim lists the claimed
// keys; those keys are ordered by deadline, so the scan stops at the first
// deadline in the future and the work is proportional to what actually expired.
//
// The one thing lazy enforcement does not do by itself is move an exhausted item
// into the dead letter when nobody ever claims again. [Queue.Sweep] is that, for
// a caller that wants it; it runs the identical pass, so it cannot drift.
//
// # The leader is the only writer, and this package says so
//
// The store's Set/Delete/Batch fail with raft.ErrNotLeader off the leader.
// This package does not hide that and does not retry it: Push, Claim, Ack, Nack
// and Sweep check leadership first and return [ErrNotLeader] — which IS
// raft.ErrNotLeader — before doing any work. A follower therefore consumes
// nothing. Consumers run on the leader, or ask the leader. Reads ([Queue.Depth])
// work anywhere and may be stale.
//
// Every method that reads before it writes issues a Barrier first. A node that
// has just become leader can have committed entries it has not yet applied to its
// local state, so without the barrier a fresh leader could read a stale ready set
// and hand out an item the previous leader already claimed.
//
// # Exclusivity
//
// A claim is exclusive for three reasons, and all three are needed:
//
//   - a write commits only on the leader, so one process holds every writer;
//   - that process reads behind a barrier, so it cannot decide on state a
//     previous leader has already moved past;
//   - inside it, every transition of every queue over one store takes one lock —
//     the [Group]'s.
//
// The lock is the group's rather than the queue's because a queue value is not
// the unit that owns a name: nothing stops a caller from opening one twice, and
// two values with a lock each are two unsynchronised claimers of one item. A
// queue is opened from a [Group] and cannot be built on its own, so a second
// writer of a name is not constructible. It also covers the one write a queue
// makes outside its own keys, which is the move into the dead-letter queue.
//
// The store has no compare-and-swap, so there is nothing else to arbitrate with.
package queue
