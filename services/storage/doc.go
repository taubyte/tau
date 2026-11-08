// Package storage is v2's object store: buckets, versions, placement and
// reclamation. This is the logic half (key grammar, version resolution, sharding,
// placement, garbage collection), each a function of values rather than a network.
// The RPC surface and byte plane attach at index.Append/Current and Blocks.
//
// Four guarantees: read-after-write for a bucket's own writer; no write silently
// discarded; opt-in monotonic reads per object, carried by the caller as one uint64;
// deterministic resolution on every replica. Not offered: linearizability,
// conditional writes, cross-key atomicity, snapshot reads, a hard quota.
//
// The current version is never stored; it is derived from an append-only version
// list (chain.go). Only store.go builds replicated keys and refs.go node-local ones.
//
// Object names are hex: caller-controlled bytes do not survive go-datastore's key
// cleaning ("a" and "a/" collide, ".." escapes the prefix), and hex is one segment,
// order- and prefix-preserving, and above 0x2f, so kvdb's "<key>/v" physical suffix
// cannot invert list order. It doubles key length.
//
// The index is sharded by a cloud constant (Shards = 256). Index and byte placement
// are separate HRW computations over one stable member set, so a bucket can exceed
// one node. Capacity is honoured only as a refusal: a node can lie itself out of a
// placement set, never in.
//
// No raft (TestRaftAppearsNowhere): pkg/raft has no conditional write, so its only
// correctness argument bounds a group to one writer, and a shard holds many buckets.
//
// Reclamation (gc.go) enumerates only this service's own block index, never the
// shared blockstore. Approximations err toward keeping: a false positive keeps a dead
// block, an incomplete walk deletes nothing. A delete drops the reference before the
// bytes; the sweep frees a block before its index row. Grace is timed from local
// observation, and possession is proven by probe, never claimed.
//
// Buckets size per instance; a shadow environment keys under its parent's
// environment; `instances.limit` rows are keyed under the bucket so they can be
// counted. A version id would better be an opaque string than a u64 (see At).
package storage
