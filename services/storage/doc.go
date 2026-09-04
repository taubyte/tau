// Package storage is v2's object store: buckets, versions, placement and
// reclamation. This round is its LOGIC HALF — the key grammar, the mutable
// pointer, sharding and placement, and garbage collection — built so that
// every one of them is a function of values rather than of a network. There is
// no RPC surface and no byte plane here yet, and the seams they attach to are
// named at the bottom of this file.
//
// # Four guarantees, and exactly four
//
//  1. READ-AFTER-WRITE FOR A BUCKET'S OWN WRITER. Every ABI caller writes then
//     reads; without it, a put returning a version is a lie.
//  2. NO WRITE EVER SILENTLY DISCARDED. This is the defect v1 shipped — a
//     read-modify-write on an LWW CRDT, with a TODO over it asking what happens
//     when two nodes update a version at the same time. The answer there is
//     that one of them vanishes with no error to either caller.
//  3. MONOTONIC READS PER OBJECT, OPT-IN, carried by the caller as one uint64.
//     A default global monotonic guarantee costs a leader or a per-read
//     handshake and buys little for data at rest.
//  4. DETERMINISTIC RESOLUTION. Every replica that has merged the same records
//     computes the same current version. It is not a nicety; it is what makes
//     (2) safe, because two surviving writes need a winner every reader agrees
//     on.
//
// NOT OFFERED, and refused rather than unimplemented: linearizability,
// conditional writes, cross-key atomicity, cross-bucket anything, snapshot
// reads, a hard quota. Each has a fence under it, and the fence bounds a bucket
// to one writer.
//
// # THE POINTER IS NEVER STORED
//
// "The current version of object X" is not a key anybody overwrites. It is a
// derived maximum over an append-only, per-writer-disjoint version list, and
// the whole of chain.go is that sentence made mechanical:
//
//	/b/<proj>/<env>/<bkt>/<inst>/o/<hexname>/<^stamp:016x>/<writer>
//
// THE WRITER IS IN THE KEY, so two nodes cannot write one key with different
// content and AGENTS.md §1 holds by construction rather than by discipline.
// THE STAMP IS COMPLEMENTED AND ZERO-PADDED, so byte order is newest-first and
// "current" is one row rather than a sort over a chain. THE VALUE CARRIES
// EVERYTHING head NEEDS, so resolution never fetches a block.
//
// WHAT HOLDS IT. The key grammar is store.go's, and it is the only file that
// builds a replicated key (refs.go builds the node-local ones; config_test.go's
// census refuses a key literal anywhere else). The resolution ORDER and the KEY
// ORDER are one order, which is a property that has to be checked rather than
// asserted — base64url is not order-preserving, so ordering by a writer's own
// bytes and ordering by its key segment are two different orders, and two
// replicas using one each would resolve one object two ways.
// TestTheKeyOrderAndTheResolutionOrderAreOneOrder drives both over generated
// writers with deliberate stamp collisions.
//
// A DELETE IS A ROW. A tombstone version, with its own stamp, acked on K
// exactly like a put — never a store-level delete on the pointer, because
// AGENTS.md §5 says a delete is an LWW write too and a delete racing a
// re-create can lose. On a version list both survive and the later stamp wins,
// visibly.
//
// # THE OBJECT NAME IS HEX, AND THAT IS THE LOAD-BEARING CHOICE
//
// An object name is caller-controlled arbitrary bytes and it is the one input
// nobody vets. Spliced raw it does not survive go-datastore's key cleaning, and
// the failures are not exotic: "a" and "a/" become one key, "a//b" and "a/./b"
// become one key, ".." eats the /o/ prefix entirely and "../../etc/passwd"
// eats the instance as well. Rejecting empty, "." and ".." segments does not
// close it — it does not catch "a/" at all — and it makes a security property
// depend on a validator matching every case the cleaner has, forever.
//
// Hex closes the CLASS: fixed width per byte over [0-9a-f], so a name of any
// bytes is exactly one segment the cleaner leaves alone. It is also
// order-preserving and prefix-preserving where the raw name is, which is what
// makes an ordered list under a byte prefix possible at all — base64url is
// neither, and gives up list ordering the moment a bucket sits under a gateway
// mount and route.strip_prefix makes the path below it the key.
//
// AND IT SURVIVES THE PHYSICAL LAYOUT UNDERNEATH, which is a second and
// independent reason none of the three source designs found. pkg/kvdb stores an
// element at <ns>/keys/<key>/v, so the bytes a backend actually sorts carry a
// "/" (0x2f) after the key. For a raw name, a key that is a prefix of another
// sorts AFTER it whenever the longer name continues with a byte below 0x2f —
// "a" against "a-b" inverts. Every hex digit is above 0x2f.
// TestTheSuffixedPhysicalKeyWouldInvertOrderForRawNames measures both
// directions. COST: 2x the key length, stated and paid.
//
// # TWO HRW COMPUTATIONS, AND THAT IS WHY A BUCKET CAN EXCEED ONE NODE
//
// The index is sharded by a CLOUD CONSTANT (Shards = 256), not instanced per
// bucket, so topic count stops tracking bucket population. A node opens a shard
// only if it is HRW-desired for it AND the shard is non-empty in the catalog's
// view, so a three-node dev cloud with five buckets opens five shards.
//
// Index placement and byte placement are SEPARATE HRW COMPUTATIONS over one
// member set, named in different resource namespaces (placement.go). The index
// for a bucket sits on R nodes; the bytes spread over the whole fleet by their
// own root CIDs. If the two tracked each other, every object of a bucket would
// land on the same R nodes its index did, and a bucket could never be larger
// than one node. TestIndexAndBytePlacementAreIndependent is that claim.
//
// Placement reads the STABLE membership set, never the live one
// (pkg/v2/membership). Liveness is a five-second question about whether a peer
// may be acknowledged to; placement is a sixty-second question about whether it
// should own data. v1 had one timescale and paid for it in thrash.
//
// CAPACITY IS HONOURED ONLY AS A REFUSAL. A node may decline to be desired and
// the next in HRW order takes the slot; nothing lets a node raise its own
// score. A node can lie itself OUT of a placement set and never IN, which is
// the only asymmetry that is safe when the figure is self-reported over gossip.
//
// # RAFT APPEARS NOWHERE
//
// Not the pointer, not placement, not the durability barrier, not quota, not
// GC, not compaction scheduling. TestRaftAppearsNowhere holds it in the import
// graph rather than in a reviewer's memory.
//
// The argument is not that raft is slow. pkg/raft offers Set, Delete, Batch and
// Merge and nothing conditional, refuses every mutation off the leader, and
// reads locally and stalely unless behind a barrier — so the only correctness
// argument available is barrier-read plus leader-write plus an in-process lock,
// and THAT ARGUMENT BOUNDS A GROUP TO ONE WRITER. services/build lives inside
// exactly that bound, which is why one node per group runs builds and the rest
// idle. A group here would have to be the shard, a shard holds hundreds of
// buckets, and every write to any of them would serialize through one node.
//
// Raft would not even buy a better conflict rule: its own split-brain heal path
// is documented as LWW with a Lamport timestamp and a wall clock breaking ties.
// It offers serialization, and serialization is what this design does not need.
//
// THE ONE STRUCTURE THAT WOULD EARN IT, written down so it is not re-argued: a
// per-bucket INDEX RANGE-SPLIT TABLE. Beyond roughly five million objects per
// gigabyte of index, a bucket's index must split, and it must split BY KEY
// RANGE rather than by hash or ordered whole-bucket listing breaks across the
// boundary. A range table is tiny, written on a split rather than on a put, and
// must have exactly one owner per range at a time — two owners of overlapping
// ranges make a listing return duplicates or gaps. HRW cannot express "the
// split happened at exactly this key at exactly this moment", and a CRDT
// resolves two concurrent splits into two overlapping range sets that both look
// valid. That is the shape where one-writer-per-group costs nothing, and where
// nothing weaker works. It is deferred, and its dependency does not exist: it
// would need a consensus-group Join seam that is not in the tree.
//
// # RECLAMATION: FIVE GATES, ONE ENUMERATION, TWO ORDERINGS
//
// THE SWEEP ENUMERATES THIS SERVICE'S OWN BLOCK INDEX AND NOTHING ELSE. The
// node's blockstore is shared with pkg/kvdb's own Merkle-DAG blocks — every
// delta of every replicated store on this node — and with every other consumer
// of the same peer. A sweep that enumerated the blockstore would delete the
// CRDT's own history. config.go's Blocks interface therefore has no
// enumeration door at all, and TestBlocksHasNoDoorForEnumeratingTheBlockstore
// keeps it that way.
//
// A FALSE POSITIVE KEEPS A DEAD BLOCK; IT CAN NEVER DELETE A LIVE ONE. That is
// the direction every approximation runs in, and it is why a Bloom filter is
// admissible: its error mode is a space leak. AN INCOMPLETE WALK DELETES
// NOTHING — if a live root's DAG cannot be walked to its leaves here, the set
// below it is unknown, and an unknown set cannot be subtracted from. It is
// self-healing rather than an operator step.
//
// TWO ORDERINGS POINT OPPOSITE WAYS AND BOTH ARE DELIBERATE:
//
//	A DELETE       the reference first, the bytes later. v1 does this
//	               backwards, freeing bytes before dropping the reference, so
//	               a crash between them leaves a live reference to bytes that
//	               are gone.
//	THE SWEEP      the block first, its index row later. A crash between them
//	               leaves a row naming a block that is gone, which the next
//	               sweep resolves for free; the other order leaves a block
//	               nothing indexes, which no sweep can ever find again.
//
// AND IT SWEEPS THE WHOLE DAG. v1's file delete removes exactly one block,
// orphaning every leaf of every multi-chunk object permanently, and there is no
// blockstore GC anywhere in v1's tree to collect them later.
//
// NO CLOCK IS TRUSTED ON THE PATH THAT DESTROYS DATA. The grace window is timed
// from when THIS node first observed a tombstone, recorded locally and
// monotonically (refs.go), never from a stamp's physical component.
//
// POSSESSION IS PROVEN, NEVER CLAIMED. Gate 5 counts holders that PASSED a
// probe, and there is no door that takes a claimed one. v1 conflated a claim
// with possession and let a data-destruction path trust a peer's self-assertion.
//
// # A CONSTRAINT ON THIS SERVICE'S STORE, and it must not be refactored away
//
// Production wires each shard's store itself rather than going through a
// factory that returns the narrow core interface. That is not preference:
// Compact, ReclaimCompacted, Repair and InternalStats are methods on the
// concrete store and are NOT on that interface, and this service is the first
// production caller of Compact. PUSHING STORAGE BACK BEHIND THAT INTERFACE
// SILENTLY STOPS COMPACTION — which is not a throughput regression but a growth
// in a new replica's catch-up cost with the cloud's LIFETIME rather than with
// its state, and that turns rebalancing, the thing this design is built around,
// into something that gets slower every month.
//
// # WHAT IS DEFERRED, AND WHAT EACH IS STUBBED BEHIND
//
//	conditional put / CAS        nothing: refused in the API. The fence
//	                             bounds a bucket to one writer.
//	hard quota, exact limits     quota.go's convergent ceiling, with the
//	                             overshoot bound stated rather than hidden.
//	index range-splitting        shard.go's constant Shards. See above.
//	sub-object striping          admission refuses an object above a node's
//	                             free space.
//	erasure coding               placement's target arithmetic.
//	regions cdn / failover       a weight field on a placement member. Only
//	                             `auto` is in this cut.
//	lifecycle expiry             a predicate hook in gc.go, off. WITHOUT IT,
//	                             versioning is unbounded disk.
//	event notifications          nothing.
//	streaming                    nothing: the owner dropped it.
//	resumable multipart          a client-stream put covers a long upload;
//	                             resuming a dropped one needs replicated
//	                             partial state.
//	provider records             deliberately not wired: every read goes
//	                             through a computed holder set.
//
// # OWNER DECISIONS: DEFAULTS TAKEN, NOT SETTLED BY WRITING (AGENTS.md §11)
//
//	O-1  unconditional per-tenant at-rest keying. DEFAULT: NOT IN THIS CUT.
//	     Taking it would close both block-layer residuals and make reclamation
//	     purely local; it costs cross-tenant and cross-bucket dedup. gc.go
//	     works without it.
//	O-3  a shard is a CROSS-TENANT REPLICATION UNIT: its R holders replicate
//	     rows for every bucket that hashes into it. That is a large improvement
//	     on v1's single cleartext registry of every project, and it is NOT
//	     per-tenant replication isolation, and it must not be described as
//	     isolation. shard.go carries it.
//	O-4  the kind's name. A service named storage with a kind named bucket is
//	     already ruled acceptable; nothing here depends on which.
//	O-5  K IS NEVER INFERRED. A single-node cloud writes Barrier: 1. The
//	     service cannot tell a fleet of one from a partition of one, so it
//	     refuses to guess. config.go, and TestKIsNeverInferred.
//	O-6  a build log's owner and lifetime. Untouched here; it decides which
//	     buckets exist, not how one works.
//
// # WHAT THE VOCABULARY CANNOT STATE THAT THIS NEEDED
//
//  1. `size` PER INSTANCE OR ACROSS INSTANCES is parked in the schema notes,
//     and it is the question a quota cannot be enforced without. Per instance
//     is free — a holder checks a local sum and coordinates with nobody. Total
//     across instances is one counter shared by every instance's holder, which
//     is a contended value, on the kind this design just argued out of
//     consensus. THIS IMPLEMENTS PER INSTANCE.
//  2. THE SHADOW-ENVIRONMENT KEY RULE is nowhere in the schema. A shadow shares
//     its parent's state, but nothing says the key's environment segment must
//     be the parent's. THIS USES THE PARENT'S, and NewBucket says so, because a
//     rule that is derivable but unwritten is an accident waiting to be
//     re-derived differently.
//  3. A VERSION ID WANTS TO BE AN OPAQUE STRING, not a u64. The packed clock
//     makes two nodes in one millisecond mint one identifier for two distinct
//     rows; resolution stays deterministic and only an exact-version read
//     becomes ambiguous. Fixing it in the ABI removes the class rather than
//     bounding it. clock.go carries the case as a test.
//  4. `instances.limit` COUNTS INSTANCES OF A BUCKET, so its rows are keyed
//     under the bucket rather than under an instance — a row keyed by the
//     instance it is filed under cannot be counted. This is a departure from
//     the brief's literal key table and the reason is here.
//
// # ONE MEASURED COST, STATED RATHER THAN ASSUMED AWAY
//
// A LIST UNDER A NARROW PREFIX IS EXACT AND ORDERED, AND IT IS NOT CHEAP. An
// object name is one segment, so a partial name is a partial segment, and every
// prefix mechanism go-datastore offers cleans the prefix and appends "/" —
// both the CRDT set's inner query and go-ds-pebble's iterator bounds. So the
// namespace narrows the scan physically to the bucket instance and the raw
// prefix selects logically within it. The brief's "no full-bucket scan" does
// not hold through this store and cannot until something offers a raw
// lower/upper bound; pebble has exactly that natively, and what stands between
// is two layers this package does not own. The cost is bounded by the BUCKET
// INSTANCE and never by the shard: a scan cannot cross a tenancy boundary, only
// waste work inside one. chain.go's scan says it at the door.
//
// # THREE BRANCHES NOTHING CAN REACH
//
// Said out loud rather than deleted, because deleting a check on a fallible API
// to make a coverage number move is the wrong trade: the record encoder cannot
// fail for this struct, the Bloom constructor errors only on an argument count
// this package does not vary, and a well-formed content identifier cannot
// contain a path separator. Each is a guard on somebody else's signature.
//
// # WHAT THIS ROUND DOES NOT BUILD, AND WHERE IT ATTACHES
//
// THE RPC SURFACE. Two service blocks: a tenant-facing one carrying the five
// declared verbs plus stat, and a node-to-node one carrying the byte barrier,
// the pointer barrier, a read mirror, the reference pull and the possession
// probe. It attaches to index.Append and index.Current for the pointer,
// Gates.ProvenHolders for the probe, and Gates.ReferencesDefinitive for the
// pull. Caller identity is the libp2p-authenticated peer, read in exactly one
// file, and every version row's writer segment must equal that peer.
//
// THE BYTE PLANE. config.go's Blocks, implemented over the node's DAG service:
// import with re-derivation of the CID from the bytes received, a local-only
// link walk over an offline session, and a delete. Local.Imported and
// Local.Reference are the rows it must write — the reference on EVERY push that
// lands here, including when the blocks were already present, because that is
// the dedup hole.
//
// THE MEMBERSHIP TRANSPORT. pkg/v2/membership.Observe is the door; the
// heartbeat topic, the incarnation and the direct dials to co-holders are the
// service round's. Membership stays on pubsub rather than RPC deliberately:
// liveness rides multi-hop gossip while a durability barrier needs a direct
// dial, and the two legitimately disagree under an asymmetric partition.
//
// THE SERVICE SHELL. New, Start, Close and the descriptor, following
// services/v2/auth: New validates and allocates and binds nothing, Start listens
// once, Close is idempotent and closes only what it started. Descriptor.Groups
// is nil.
package storage
