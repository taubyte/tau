// Package distributed backs the `sql` data model with tau's own engine:
// mushql, a pure-Go SQLite. Its sibling pkg/database/engines/sql is the other
// half of the model — Postgres and MySQL over a DSN, on a server somebody else
// runs.
//
// # A SQL database is a FILE, which is the whole of why this is not engines/kv
//
// Every resident engine before this one kept its entries behind
// database.Store's four-method door, because a kv, a document and a vector are
// all rows addressed by a key. A SQLite database is not rows addressed by a
// key: it is a file, and the engine underneath wants a path on a disk. So this
// engine is handed a DIRECTORY provider rather than a store provider — see
// Files — and the replication database.Store got for free from the kvdb
// underneath is mushql's own (github.com/samyfodil/mushql/replication).
//
// # It replicates in the mode the database declared
//
// Every database is opened with replication.Open, so every table and the
// schema itself replicate — including the tables a guest creates after the
// database is open, which is the only way a tau `sql` database ever gets one.
// The copies of one database meet on one topic, derived from the database's
// own prefix (topicOf); how they meet is the composer's, through WithSync.
//
//   - crdt: every node writes, and the copies converge. Constraints hold on a
//     node's own writes; a peer's rows are applied without them, so two nodes
//     writing one UNIQUE value keep the newer row and hold the other back until
//     the value is free. That is mushql's contract and it is stated, not hidden.
//   - raft: REFUSED BY NAME (ErrRaft). mushql commits a raft database through
//     the caller's consensus log — replication.Leader with a Quorum, one entry
//     per commit applied on every node in order — and pkg/raft's state machine
//     is key-value only. Serving it as crdt instead would be a weaker promise
//     than the one declared.
//
// # What it does not do yet
//
// A node does not register this engine: nothing constructs it with a sync
// transport over the node's host and pubsub, so a node's `sql` databases are
// still served by whatever engine it does register.
package distributed
