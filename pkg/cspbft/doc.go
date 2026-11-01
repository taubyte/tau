// Package cspbft is Byzantine agreement on a sequence of opaque proposals, as a
// state machine: Step, Timeout, Checkpointed, Installed and Restore go in, Actions
// come out. No goroutine, socket, disk or clock read, so an interleaving ("seed 41,
// n=7, primary equivocates on sequence 12") is a table entry.
//
// It is for replicas that do not trust one another (f Byzantine out of 3f+1, every
// vote a checkable signature); inside one cloud pkg/raft and pkg/kvdb suffice.
//
// The agreement mechanism is Castro-Liskov PBFT (OSDI '99): three phases, water
// marks (h, h+L] with L = 2K, checkpoints every K, VIEW-CHANGE carrying C and P,
// NEW-VIEW carrying V and O with null-request gaps, and f+1 VIEW-CHANGEs pulling a
// lagging replica forward. Two departures: digital signatures instead of MAC
// authenticators, since only a signature proves equivocation to a third party; and
// none of section 5's optimizations.
//
// The CS-PBFT paper is paywalled and was not read; only the title's seam, leader
// selection by score, is here. A Scorer's contract:
//
//  1. It reorders, never replaces: primary(v) = perm[v mod n] over all n replicas,
//     so every replica still takes its turn.
//  2. perm changes only at a stable checkpoint.
//  3. A NEW-VIEW carries the epoch and H(perm); a mismatch is
//     ErrRotationMismatch, not a silent stall.
//  4. A Scorer may read only application state as of Snapshot.StateDigest, never
//     local observation (RTTs, timeouts), or correct replicas disagree on the
//     primary.
//  5. Deterministic integer arithmetic with a ReplicaID tie-break; no floats.
//  6. Garbage (wrong length, duplicates, unknown ids, nil, panic) degrades to
//     round-robin with a Notice{ErrScorerPermutation}.
//  7. Only rotation.go holds the Scorer; quorum code cannot see it.
//
// The cost: up to 2f consecutive faulty primaries instead of f, at most once per K
// sequence numbers. The default RoundRobin is textbook PBFT.
//
// Equivocation is detected and handed out as Evidence, not fed into the rotation
// (who has seen a proof is local). A caller submits it as a request; once committed,
// the Scorer reads it from agreed state.
//
// The caller owes three things:
//
//  1. A Persist action is durable before any later action in the same slice is
//     performed. Step places a vote's Persist before its Broadcast; on restart the
//     records are replayed through Restore before the first Step.
//  2. A Step returning ErrFutureView is retried once this replica enters that
//     view, or a new primary's first assignment can be lost forever.
//  3. Clients retransmit until they see enough replies.
//
// What a replica remembers about executed requests ends at the stable checkpoint,
// the one point every replica agrees on, so replicas that took state by transfer
// forget the same digests at the same moment; the application's reply table
// suppresses a second execution.
//
// No message carries a wall clock. The core emits SetTimer/CancelTimer and is told
// by Timeout. Message.Serial is a per-client counter, not a time.
//
//	f = Faults(n) = (n-1)/3            the most faulty replicas tolerated
//	q = Quorum(n) = ceil((n+f+1)/2)    any two quorums share a correct replica
//	prepared      1 PRE-PREPARE + q-1 PREPAREs from distinct others
//	committed     prepared + q COMMITs from distinct replicas, self included
//	stable        q matching CHECKPOINTs
//	hearsay       f+1 VIEW-CHANGEs above my view FROM OTHERS means I am behind
//	view budget   2f+2 future views remembered PER SENDER, smallest first
//
// f+1 means at least one is correct; q means any two such sets share a correct
// replica. Supported n is [4, 16]: view change is O(n^3) bits.
//
// Not here, deliberately:
//
//   - Aborting a view change. Having sent a VIEW-CHANGE for v', a replica never
//     votes below it, or it could prepare a digest the next view's O set misses. A
//     replica whose timer fires alone therefore waits for the next view change,
//     kept current by state transfer. Removing that needs a separate intent message
//     before the VIEW-CHANGE, a protocol change.
//   - Reconfiguration: (n, f) is fixed. Eviction by accusation lets f faulty
//     replicas plus one misfire evict correct ones; demotion in perm suffices.
//   - Weighted voting: the fault model bounds the count, not weight.
//   - A wire format: signatures are over Preimage(m), never encoded bytes.
//   - An application interface or a driver: Execute carries what a reply needs,
//     and Example is the caller's loop.
package cspbft
