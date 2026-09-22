package primitives

import "time"

// config.go is what an operator declares about a primitives node, and what this
// service holds itself to when nobody declared anything.

// namespace is the raft namespace every queue's items and every log's head live
// in.
//
// ONE NAMESPACE FOR THE WHOLE SERVICE, and not one per resource. A group per
// queue is a leader election and a replicated log per declared resource, which
// is the cost node/queue.go already refused to pay; pkg/queue's key layout
// keeps them from seeing each other, and [Spec.Prefix] carries the deployment
// so two projects that both declare `jobs` never meet.
//
// WHAT THAT COSTS, SAID RATHER THAN IMPLIED: placement cannot move one queue
// without moving all of them, so for a queue placement is a decoration until
// group-per-resource lands. A log's records do not live here, so nothing about
// them is decided by this line.
//
// AND IT IS THE ONLY ONE NOW. The worker used to hold a raft group of its own
// for the same project's queues, its logs' heads and its crons' slot claims,
// so a cloud ran two groups over the same coordinated state — which is the pair
// pkg/raft reconciles last-writer-wins, the resolution `consensus: raft` exists
// to avoid. That group is gone and everything it held is keyed under this one:
// pkg/queue's own layout for a queue's items, `logs/` for a log's head and
// `cron/fire/` for a schedule's fires. A SCHEDULE'S POSITION IS NOT HERE AND IS
// NOT ANYWHERE: the clock's memory is one leader's process, and cron.go states
// the trade that buys.
const namespace = "/tau/primitives/v1"

// recordStore is the replicated store a log's records live in.
//
// ONE STORE FOR EVERY LOG, keys prefixed by [Spec.Prefix] — the shape
// node/log.go chose and the one node/cache.go states the reason for. A store
// per log would make placement mean something for logs, and it would also be a
// pubsub topic and a merkle DAG per declared log on every node that serves one.
// That trade is worth making the day something ACTS on the placement; until
// then it would be a cost with no behaviour attached. See retention.go.
const recordStore = "primitive-logs"

// rebroadcastSeconds is how often the CRDT under the record store re-announces
// its heads. It is node/locate.go's number, for the same reason: a store that
// re-announced slower would take longer to heal a node that missed a write.
const rebroadcastSeconds = 5

// applyTimeout bounds one replicated write. It was node/cron.go's number and it
// is a bound on a REQUEST rather than on a cluster: a write that has not
// committed in five seconds is one the caller should be told about.
const applyTimeout = 5 * time.Second

// DefaultSweep is how often this node takes a turn: retention on the logs it
// leads, the claim/dead-letter pass on the queues, and a look at the clock.
//
// IT IS THE ONLY PERIOD THIS SERVICE HAS. There used to be a second, shorter
// one for the clock, because the clock did per-schedule work on every tick of
// it. The leader's turn is now a comparison of two minutes and, at most once a
// minute, one tasking — so a turn with no minute to hand out costs nothing and
// there is nothing left for a second number to buy.
//
// FOR THE LIFECYCLE IT IS NOT A HEARTBEAT. Nothing waits on a sweep — a claim
// that expires is enforced by the next Claim whether or not this ran, which is
// pkg/queue's lazy rule and the reason there is no second lifecycle to get
// wrong. What this adds is the half laziness cannot do: a log nobody appends
// to, and a dead letter nobody claims from.
//
// FOR THE CLOCK IT IS THE LATENESS OF A FIRE. A cron expression's finest grain
// is a minute, so this decides how promptly a new minute is noticed and not
// what a schedule can say — and an operator who wants a fire tighter to its
// slot shortens it.
const DefaultSweep = 30 * time.Second

// MaxRecord is the largest log record this cloud takes, and MaxItem the largest
// queue item.
//
// THE CEILING IS THE ABI'S AND NOT A POLICY. A record and an item each cross as
// one `list<u8>`, so each is materialised whole in the guest's memory on the way
// out and in the node's on the way in. MaxRecord is node/log.go's number,
// unchanged.
//
// MaxItem IS NEW, and node/queue.go had none: a guest's bytes went straight to
// Push, where they live INLINE in a state machine pkg/raft snapshots whole and
// every voter holds in memory. An unbounded item there is an unbounded heap on
// every node in the cloud, which is a worse failure than a refused send.
const (
	MaxRecord = 1 << 20
	MaxItem   = 1 << 20
)

// maxPage is both the default page and the ceiling on a larger ask, for a log
// read and for a claim. A caller that wants everything asks repeatedly.
const (
	maxPage  = 512
	maxClaim = 64
)

// DefaultDeadLetterRetention is how long an item that gave up is kept, and how
// many bytes of them.
//
// A BOUND RATHER THAN FOREVER, WHICH IS THE OPPOSITE OF THE LOG'S DEFAULT, and
// the asymmetry is the point. A log's "absent means forever" is safe because a
// log is a thing somebody reads. A dead letter is not: the default one is
// derived from its queue's name, so it is not a declared resource, so no
// consumer can be declared for it and none ever will be — and every item in it
// sits inline in a state machine every voter holds in memory. Forever there is
// not a promise to an author, it is an unbounded heap on every node in the
// cloud with no word an author can write to stop it.
//
// SO THE SERVICE HOLDS THE INVARIANT ITSELF rather than waiting for an operator
// or an attribute. The vocabulary still owes the author's half — a
// `queue/dead_letter/retention` pair reusing the log's words — and until it
// exists these are what every cloud gets. A week is long enough that an
// operator looking into a failure finds the evidence, and short enough that a
// workload failing every item does not take the cloud down over a weekend.
var DefaultDeadLetterRetention = retention{age: 168 * time.Hour, size: 64 << 20}
