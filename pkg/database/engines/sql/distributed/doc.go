// Package distributed backs the `sql` data model with tau's own engine, musql, a
// pure-Go SQLite. A SQLite database is a file, not keyed rows, so this engine gets a
// directory provider (Files) rather than a store, and replication is musql's own.
//
// Every database is opened with replication.Open, so tables a guest creates later
// replicate too. Copies meet on a topic derived from the prefix (topicOf), joined
// through WithSync.
//
//   - crdt: every node writes and copies converge. Constraints hold on a node's own
//     writes; a peer's rows apply without them, so two nodes writing one UNIQUE
//     value keep the newer row and hold the other back until the value is free.
//   - raft: refused by name (ErrRaft): musql needs a consensus log applying opaque
//     entries in order, and pkg/raft's state machine is key-value only.
//
// No node registers this engine yet.
package distributed
