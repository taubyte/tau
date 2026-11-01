// Package primitives serves the `sync` kind's two types, `queue` and `log`, and the
// clock. It owns its raft namespace, the store logs' records live in, the retention
// that ends them, and the sweep that moves queue work along.
//
// The clock: once a minute the namespace's raft leader hands one window to one node
// (CronTask) and does nothing else, so its cost does not grow with the number of
// crons. The tasking is the exactly-once; the leader's last handed-out minute lives
// in its process, so a leadership change loses at most one minute's slots (cron.go).
// A fire is an item in a platform queue, retried and dead-lettered like any other.
// Which crons exist, how a fire reaches a workload and which node to task are the
// composer's: WithSchedules, WithFire, WithTasking.
//
// Not the database service: a queue has no key and a log's position is issued, not
// chosen. Not storage: records and items are never named, listed or metered.
//
// The caller names a primitive and never describes one: visibility timeout, retry
// budget, dead letter, retention and consensus are read from the compiled project.
//
// Every write is ordered by the leader and refused elsewhere, with the leader's name.
// The clock's tasking is the exception: not a request-path write, it is forwarded
// once.
//
// No authorization: any peer that can reach this service can use any project's
// primitives. Swarm membership, enforced by the cloud's CA, is the boundary.
package primitives
