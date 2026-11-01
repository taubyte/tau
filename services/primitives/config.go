package primitives

import "time"

// namespace is the one raft namespace for every queue's items, every log's head
// (`logs/`) and every schedule's fires (`cron/fire/`), not one per resource: a group
// per queue costs an election and a log each. pkg/queue's layout and Spec.Prefix keep
// deployments apart. Placement therefore cannot move one queue alone.
const namespace = "/tau/primitives/v2"

// recordStore is the one replicated store all logs' records live in, keys prefixed by
// Spec.Prefix; a store per log would cost a topic and a DAG per log for placement
// nothing acts on yet.
const recordStore = "primitive-logs"

// rebroadcastSeconds is how often the CRDT under the record store re-announces
// its heads. It is node/locate.go's number, for the same reason: a store that
// re-announced slower would take longer to heal a node that missed a write.
const rebroadcastSeconds = 5

// applyTimeout bounds one replicated write: a bound on a request, not the cluster.
const applyTimeout = 5 * time.Second

// DefaultSweep is how often this node takes a turn: retention on the logs it leads,
// the claim/dead-letter pass on the queues, and a look at the clock. Nothing waits on
// it (an expired claim is enforced by the next Claim); it covers the logs nobody
// appends to and dead letters nobody claims from. For the clock it is how late a fire
// can be.
const DefaultSweep = 30 * time.Second

// MaxRecord is the largest log record and MaxItem the largest queue item. Each
// crosses the ABI as one list<u8>, materialised whole, and an item lives inline in a
// state machine every voter holds in memory.
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

// DefaultDeadLetterRetention bounds a dead letter by age and size. A dead letter is
// derived, not declared, so no consumer drains it and it sits in every voter's
// memory; forever would be an unbounded heap. A week leaves the evidence and survives
// a failing workload over a weekend. The vocabulary has no author setting for it yet.
var DefaultDeadLetterRetention = retention{age: 168 * time.Hour, size: 64 << 20}
