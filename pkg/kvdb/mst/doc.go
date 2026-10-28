// Package mst is a Merkle search tree (Auvolat and Taïani, SRDS 2019): an
// ordered map of byte keys to byte values whose shape is a pure function of
// its contents, so two trees holding the same entries have the same root CID
// whatever order they were written in. It knows nothing of what the keys and
// values mean.
//
// A Tree is an immutable handle on a root CID over an ipld.DAGService. Put
// and Delete return a new Tree, write only the nodes on the changed path,
// and share every other node with the tree they started from. Nothing in
// this package ever removes a node: nodes are shared between roots, so
// reachability (Reachable) and anything built on it is the caller's.
//
// Every read goes through the DAGService. A CID being known is never taken
// to mean its block is held: an absent node fails the operation that needed
// it with the store's not-found error, and Missing finds the nodes of a tree
// that are not held, so a caller can complete its closure first.
//
// # Format profile 1
//
// This is the whole definition of a tree's bytes. Anything that changes it
// is a new profile, with a new value of Node.format.
//
// Keys are compared as unsigned bytes (bytes.Compare). A tree holds each key
// at most once; a Put of a held key replaces its value, and Build refuses a
// key that is not greater than the one before it. A nil and an empty value
// are the same value.
//
// The level of a key is the number of leading zero 4-bit digits of
// SHA-256("tau/mst/v1/level" || key), a 16-byte ASCII domain prefix followed
// by the key. A key has level >= l with probability 16^-l, so a node holds
// about 15 entries and has about 16 subtrees.
//
// The empty tree has no node: its root is cid.Undef. A non-empty tree's root
// node holds every key of the highest level present. The keys between two
// adjacent entries of a node, and those before its first and after its last,
// form that slot's subtree, laid out by the same rule: its root node holds
// the keys of the highest level among them, which may be any level below the
// parent's. Levels with no key in a slot are skipped, so no node has zero
// entries, and a slot with no keys has no subtree.
//
// A node is a dag-pb block (CIDv1, codec dag-pb 0x70, multihash sha2-256),
// built as kvdb builds its delta blocks: Data is the protobuf Node in
// pb/node.proto, and Links are the node's subtrees in key order, each with
// an empty Name and a Tsize of 0. dag-pb's encoder sorts links by Name with
// a stable sort, so with every Name empty the order written is the order
// added.
//
// Data is the Node message as Go protobuf marshals it deterministically:
// fields in field-number order, varints in their shortest form, and no field
// written that holds its zero value (proto3 implicit presence; nothing is
// optional). Its fields:
//
//	format        1 for this profile.
//	level         the level of every key in the node.
//	left          whether a subtree precedes the first entry.
//	left_summary  the summary of that subtree (below), else empty.
//	entries       one Entry per key, in ascending key order.
//
// and an Entry's:
//
//	shared         the length of the longest common prefix of this key and
//	               the previous entry's key; 0 for the first entry.
//	suffix         the key's bytes after those shared ones.
//	value          the value.
//	right          whether a subtree follows this entry.
//	right_summary  the summary of that subtree, else empty.
//
// A node's subtrees are named by position: the n-th set flag, taking left
// first and then each entry's right in order, is the n-th link. The number
// of links equals the number of set flags. A node with unknown fields, no
// entries, keys out of order, a shared prefix longer than the previous key,
// or a format other than 1 is refused.
//
// A tree may carry a Summarizer. A subtree's summary is then the fold, with
// Merge, of Leaf(key, value) over its entries in key order; Merge is
// associative, so that is the same value however the subtree is shaped, and
// it is a pure function of the subtree's contents. Without one, every
// summary is empty. Summaries are part of the bytes: the same entries under
// two summarizers are two different trees.
package mst
