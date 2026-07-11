package hoarder

// Stream command names and body keys for the hoarder data plane.
const (
	// StashCommand is the DefineStream route: a command phase (ready ack)
	// followed by a raw phase carrying a framed header + the file bytes.
	StashCommand = "stash"

	// StashHeader is the framed command name sent on the raw phase before the
	// bytes, carrying the keys below.
	StashHeader = "push"

	// HoarderCommand is the classic command route for rare/list.
	HoarderCommand = "hoarder"

	BodyAction  = "action"
	BodyCid     = "cid"
	BodyTarget  = "target"
	BodyOwner   = "owner"
	BodyFanout  = "fanout"
	BodyProject = "project"
	BodyApp     = "application"
	BodyMatch   = "match"
	BodyPeers   = "peers"

	ActionRare     = "rare"
	ActionList     = "list"
	ActionReplicas = "replicas"
	ActionStatus   = "status"
	ActionLoad     = "load"
	ActionUnload   = "unload"
)

// KVDBCommand is the remote data-plane route. Body carries {kind, project,
// application, match, branch, kvop, key/value/prefix/ops/limit/cursor} and the
// handler operates on the loaded instance kvdb (first-touch if unplaced).
const KVDBCommand = "kvdb"

const (
	BodyKind   = "kind"
	BodyBranch = "branch"
	BodyKVOp   = "kvop"
	BodyKey    = "key"
	BodyValue  = "value"
	BodyPrefix = "prefix"
	BodyRegexs = "regexs"
	BodyOps    = "ops"
	BodyLimit  = "limit"
	BodyCursor = "cursor"
	BodyValues = "values"
	BodyKeys   = "keys"
	BodySize   = "size"
	BodyCode   = "code"
	// BodyServedBy is the peer ID of the hoarder that served a kvdb request, so
	// the client can pin to it for read-your-writes (esp. after a first-touch,
	// where the client didn't pick the peer).
	BodyServedBy = "servedBy"
	// BodyNoBarrier marks a write as a K=2 replication push from another hoarder
	// — the receiver applies it locally without re-replicating (no barrier loop).
	BodyNoBarrier = "noBarrier"
)

// kvdb sub-operations (Body[BodyKVOp]).
const (
	KVGet       = "get"
	KVPut       = "put"
	KVDelete    = "delete"
	KVList      = "list"
	KVSize      = "size"
	KVBatch     = "batch"
	KVListRegex = "listRegex"
	KVSync      = "sync"
)

// Typed result codes carried in the response's BodyCode field for control-flow
// signals the client routes on (never as free text). A response with no BodyCode
// is a success.
const (
	CodeNotReplica   = "not-replica"   // sent to the wrong node; BodyPeers has the live replicas
	CodeNotFound     = "not-found"     // key absent
	CodeOverCapacity = "over-capacity" // admission rejected the write
)
