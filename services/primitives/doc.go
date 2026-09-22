// Package primitives serves a project's coordination primitives — the `sync`
// kind's two types, `queue` and `log` — and the reason it exists is that
// nothing sat between them and the guest.
//
// # What v1 did
//
// node/queue.go opened a raft cluster inside the worker's own process,
// node/log.go put every log's records in one store every node carries, and
// node/cron.go claimed a schedule's slot in that same cluster. So the thing
// that decides who writes next was a property of whichever machine happened to
// run the code, and the lifecycle of a queue — its claims, its retries, what
// happens to an item that gave up — was owned by nobody.
//
// THOSE THREE FILES ARE GONE, and this package's prose cites them throughout.
// Every such citation is HISTORY: it names where a rule was learned, or the
// defect that taught it, and the path is there so the commit that removed it
// can be found. Nothing in this tree reads them; a worker node opens no raft
// group of its own at all.
//
// # What this service owns
//
// Its own raft namespace, the store a log's records live in, the retention that
// ends them, and the sweep that moves a queue's work along. That is the whole
// of "it stores on disk": a log's records and a queue's items, both small,
// neither addressed by name.
//
// # And the clock, which is the same question again
//
// A cron fires exactly once across a fleet because ONE node is asked to fire
// it, and "which node" is a question raft already answered when it elected a
// leader for this namespace. So the clock lives here too, and it is one
// sentence long: once a minute the leader hands ONE minute to ONE node —
// "minute T is yours: generate and distribute the cron jobs" — and that is all
// it does. It reads no position, opens no queue and looks at no schedule, so
// its cost is one tasking whether the cloud holds ten crons or ten million.
//
// THE TASKING IS AN RPC — CronTask, carrying the window and nothing else — so
// the node that does the walk is not the node that owns the minute, and in a
// fleet it usually is not even the same machine. What this service cannot know
// is which crons exist, how to reach a workload and WHICH node to hand a window
// to; all three are injected by the composer: see WithSchedules, WithFire and
// WithTasking.
//
// A FIRE IS AN ITEM AND NOT AN INVOCATION, which is what a queue was always
// for. The tasked node pushes one per thing a slot owes into a platform queue
// of the schedule's own and claims it back, so a fire that fails is retried,
// backed off and finally dead-lettered rather than being a log line and a
// consumed slot.
//
// AND THERE IS NO REPLICATED RECORD OF A TICK. The tasking is the exactly-once,
// so nothing has to be written down for it and nothing has to be atomic with
// the work. What that costs is stated where it is paid: the minute a leader has
// already handed out lives in that leader's process, so a leadership change
// loses at most one minute's slots. See cron.go.
//
// # Why it is neither the database service nor the storage service
//
// `database` is a closed set of data MODELS — kv, sql, document, vector — each
// with its own operation surface, reached by naming a key. A queue has no key
// and a log's position is ISSUED rather than chosen, so neither is a value
// pkg/database could carry: a Spec of type "queue" is something it is written
// to refuse.
//
// `storage` is bytes an author NAMES, reads back by name, lists, meters and
// collects against a quota. A log record and a queue item are bytes the
// platform keeps as a consequence of ordering something: never addressed as
// objects, never listed, never metered. Same disk, different question — and it
// is why these are small when storage's are not.
//
// # The caller names a primitive; it does not describe one
//
// A call carries a deployment and a resource id and nothing else. The
// visibility timeout, the retry budget, the dead letter, the retention and the
// consensus are read HERE, out of the compiled project. That is the difference
// between a service and a proxy: a caller that described the queue could
// describe a different one — shorten somebody else's visibility timeout, point
// a dead letter at a queue it may not write — and the service would have no way
// to tell. node/queue.go read all of it off the grant, which is the caller's.
//
// # One writer, and this service refuses rather than forwards
//
// Every write here is ordered by the raft leader of this service's namespace,
// and a write that lands anywhere else is REFUSED and not forwarded — pkg/queue's
// rule, and node/queue.go states the reason at length: a follower consumes
// nothing, and forwarding would be a second write path with its own failure
// modes. What is different here is that the refusal carries the leader's name,
// so a caller has somewhere to go rather than only something to retry.
//
// THE CLOCK'S TASKING IS THE ONE EXCEPTION, and it is an exception because it
// is not a write. A window is one message a minute with nobody waiting on it,
// so a node handed one it cannot commit sends it on to the node its own group
// names — once, counted, and refused if it has already been sent on. A guest's
// send is on the request path and per call, where a hop it did not ask for is
// latency it can neither see nor retry around. See cron.go.
//
// # What it does NOT do
//
// AUTHORIZATION. Any peer that can reach this service can send to and read any
// project's primitives. That is v2's OSS trust model stated where it applies —
// swarm membership is the boundary and the private swarm key enforces it — and
// the register, the secrets service and the database service say the same.
package primitives
