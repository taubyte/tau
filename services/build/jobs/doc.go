// Package backend is build's private half: the queue (raft), the jobs (events in
// kvdb, events.go) and the run, served as build.proto's QueueService and
// JobService to other tau nodes
// over p2p/rpc and reachable no other way. The public half, taking webhooks off
// the internet, is services/build/ingress.
//
// Four rules:
//
//  1. Every RPC is leader-only, reads included; a follower answers
//     CodeUnavailable (leader.go). No forwarding, no leader cache, no staleness.
//  2. Ack is the commit point. A run's terminal event is written only after the
//     queue accepted the ack, the one compare-and-swap here: the loser of a
//     leadership flap gets ErrStaleDelivery and publishes nothing.
//  3. The claim's deadline bounds the run (less a margin, capped by
//     BuildTimeout); a run outliving its claim is a second claimer.
//  4. Cancel is an event, and a job's read and the entry it decides are one
//     section (section.go's jobLock), so Cancel and Retry on rpc goroutines
//     cannot interleave with the claim loop. Across nodes nothing is locked: the
//     fold (events.go) gives every node the same job from the same events.
//
// A section's write is made while the deciding read's section is still held
// (runtime: a handle dies when withJob returns), by the body that took that read
// (compiler: every write is a method on the handle), and only about the job the
// read was on (compiler for the subject; for events.go's key grammar, the
// censuses in population_test.go).
//
// New binds no port and opens nothing; it takes a host, the queue's raft store
// (pkg/queue's Store), the kvdb database the jobs live in, and one question for
// auth. The node supplies the Runner
// and the storage service as Assets (instance/build.go).
//
// A push is a plan job: the runner reads which environments the commit feeds, and
// each becomes a job of its own (id over repository, ref, commit, environment),
// since a build can depend on its environment. A plan keeps no log; what it said
// opens each environment job's.
//
// A log is a file, and only its cid is replicated: while the build runs it is an
// append-only file this node owns (services/build/livelog); followers read it at
// an offset (server.Follow); when the build ends it is LZW-compressed into the
// logs instance of its environment's assets bucket between settle's two sections,
// and its cid lands on the record, only if the whole log was stored. An artifact
// is a version of its package's instance in the same bucket.
//
// Known gaps: a record's identity fields other than ID are whatever the body
// hands over; a log can reach the asset store with no cid reaching any job if
// leadership is lost after the store takes it (bounded by PublishTimeout; the job
// is rebuilt); and nothing bounds how much a build stores. A finished job is
// erased after JobRetention, unless what it built is still what a deployment
// runs: that is resolved through the job.
package jobs
