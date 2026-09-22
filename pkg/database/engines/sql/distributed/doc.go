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
// underneath has to be arranged here instead.
//
// # IT DOES NOT REPLICATE YET, AND THE REASON IS NOT ON THIS SIDE
//
// mushql replicates through a CRDT oplog, and the entry point is
// `crdt.Wrap(ctx, dsn, site, tables)`: the tables to sync are named UP FRONT,
// must already exist, and the schema of that set is fingerprinted into the
// database the first time it is wrapped. A tau `sql` database declares a size
// and a consensus and NO TABLES — every table it will ever hold is created by
// a guest's `CREATE TABLE` after this engine already has the database open.
//
// The three facts that close the door, each pinned by a test in schema_test.go:
//
//   - wrapping a table that does not exist yet is refused, so the engine
//     cannot name the guest's tables before the guest creates them;
//   - wrapping no tables at all succeeds and captures nothing, so opening that
//     way replicates nothing, silently;
//   - wrapping again with the grown set — the only way mushql offers to start
//     syncing a table it was not told about — is refused by the stored schema
//     fingerprint, permanently, and nothing exported resets it.
//
// So an engine that wrapped anyway would either replicate nothing or fail to
// reopen the database after the first restart. This one therefore serves SQL
// from the node's own disk and says so, rather than carrying a replication
// seam that cannot be honoured. A NODE MUST NOT REGISTER IT AS THE `distributed`
// ENGINE until mushql can be told about a table after the first wrap.
//
// # AND REPLICATING IT WITH RAFT NEEDS A SECOND THING MUSHQL DOES NOT HAVE
//
// The schema fingerprint is the blocker for replicating at all. Replicating
// through tau's raft rather than through mushql's own anti-entropy needs one
// more: a seam consulted BEFORE the transaction commits, which assigns the
// op's identity and can refuse it. mushql's crdt layer stamps the identity
// from a local counter and consults its transport after the commit, which is
// the right order for a CRDT and backwards for raft.
//
// authority.go states that seam as Go, with tau's half of it written out and
// tested; raft_hypothesis_test.go is the evidence, measured against mushql as
// it ships. Neither modifies mushql.
package distributed
