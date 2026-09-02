// Package backend is build's private half: the queue, the job records and the
// run. It serves both of build.proto's service blocks — QueueService and
// JobService — to other tau nodes over p2p/rpc, and it is reachable no other
// way. The public half, which takes webhooks off the internet and calls in
// here, is services/build/frontend.
//
// It is v1's patrick (the job store and the queue) and v1's monkey (the thing
// that runs a job) as ONE process, which is what deletes most of what those two
// spent their code on: patrick's dequeue/isAssigned/timeout RPCs existed only
// because claiming work and reporting a stale claim had to cross a service
// boundary, and monkey's cancel round trip existed only because the record and
// the run lived in different processes. Here they are two lines in one.
//
// # Four rules, and everything else follows from them
//
//  1. EVERY RPC IS LEADER-ONLY, READS INCLUDED, and a follower answers
//     CodeUnavailable. See leader.go — it is one rule rather than five, and it
//     is what removes forwarding, a leader client cache and the staleness
//     question in one go.
//
//  2. ACK IS THE COMMIT POINT. A run's terminal record is written only after
//     the queue has accepted the acknowledgement, because Ack is the only
//     compare-and-swap this service has: the loser of a leadership flap gets
//     ErrStaleDelivery and must publish nothing. v1 wrote its results first and
//     asked whether it still owned the job afterwards, if at all.
//
//  3. THE CLAIM'S OWN DEADLINE BOUNDS THE RUN. A run that outlives its claim is
//     a second claimer with the same job, on one node as much as on five —
//     the queue's own repair() would hand the item back and the very next cycle
//     of this loop would take it. So the run context's deadline is the claim
//     deadline less a margin, capped by the operator's BuildTimeout.
//
//  4. CANCEL IS A RECORD WRITE, not a message, and A JOB RECORD'S READ AND THE
//     ENTRY IT DECIDES ARE ONE SECTION. A queued job needs no special case
//     because the claimer re-reads the record before it runs — but re-reading
//     is only worth something if nothing writes between the read and the entry
//     it decided, and Cancel and Retry answer on rpc goroutines while the claim
//     loop runs on its own. raft orders their entries and cannot see that the
//     second decided on a stale read. queue.go's jobLock is that section, its
//     boundary is stated there, and population_test.go is the list of every
//     place it has to be taken. v1 polled a claim record thirty times at
//     two-second intervals, blocking the request for up to a minute, and still
//     could not cancel a job that had not been picked up yet.
//
// # What it is built from
//
// A host to answer on, a replicated store, and one question it can ask auth.
// Nothing else, and nothing it constructs itself: New binds no port, opens no
// database and invents no libp2p identity, so a node can construct every
// service before starting any of them (service-conventions.md §2a) and this
// package's own tests stand it up on mocknet.GenPeer() and raft.NewMockCluster()
// with nothing else running.
//
// The store is ONE store — pkg/v2/queue's Store, method for method — so the
// fact that decides a claim and the fact that records it are written by one
// writer against one log. Two stores is exactly the seam v1's /jobs/ record and
// its separate /assigned/ row fell through.
//
// # What it deliberately does not do
//
// It does not run a build. Runner, Work and Sink are the seam, and the reason
// is not effort: v2 has no tns schema to publish a compiled config to and no
// storage contract to stash bytes at, and reaching for v1's clients would
// import the cbor stack v2 exists to leave. Everything around the run —
// idempotency, claiming, attempts, cancellation, the before..after anchor, the
// per-attempt log and asset rows — ships now, so the runner is a drop-in.
//
// It also does not serve log or artifact BYTES. v1's GET /logs/{cid} is an open
// IPFS gateway gated only by a token being valid, and its /download route has
// no authorization at all; when those come back they are per-job routes on the
// frontend, authorized like Get and checked against that job's own maps.
package backend
