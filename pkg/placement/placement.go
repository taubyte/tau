// Package placement answers "which members own this resource" by computing it
// rather than agreeing on it.
//
// Rendezvous hashing (HRW): every member is scored against the resource and the top
// `target` win, so every node with the same membership list reaches the same answer
// with no leader or election. Storage, database and cache share it.
//
// Determinism: Score is a pure function of two strings and Desired sorts with a total
// tie-break, so nothing depends on map order or input order.
//
// Minimal reshuffle: when a member leaves, only the resources it held move, each by
// one slot; when one joins, a resource either takes it or is untouched. Hashing mod
// the member count would re-home nearly the whole keyspace on every change.
//
// Member.Accepts is a decline and the only thing a member says: a node can lie
// itself out of a placement set, never in. There is no capacity, weight or load to
// understate.
//
// A region weight would enter as a field on Member and a change to score()
// (weighted rendezvous, -weight/ln(h/2^64)), with no change to Desired's shape.
//
// Callers name their own resource strings: storage places a shard's index and an
// object's bytes under unrelated strings, which lets a bucket outgrow one node.
package placement

import (
	"cmp"
	"encoding/binary"
	"slices"

	"github.com/cespare/xxhash/v2"
)

// Member is a peer as placement sees it: an identity, and one bit that can only ever
// remove it.
type Member struct {
	// ID is the member's identity — a libp2p peer id in every caller so far,
	// but this package only ever hashes and compares it, so it is a string.
	ID string

	// Accepts is false when the member has declined placement: it is full, it
	// is degraded, or it is shutting down. A declining member is skipped and
	// the next in ranking order takes the slot.
	Accepts bool
}

// Score is the rendezvous weight of one (resource, member) pair. The strings are
// length-delimited rather than separated by a byte, so ("a\x00b", "c") and
// ("a", "b\x00c") score differently.
func Score(resource, member string) uint64 {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(resource)))
	var d xxhash.Digest
	d.Reset()
	d.Write(n[:])
	d.WriteString(resource)
	d.WriteString(member)
	return d.Sum64()
}

// Rank returns every member ordered by descending score, ties broken by
// ascending ID so the order is total. Declining members are ranked like any
// other — Rank is the whole ordering, and Desired is what applies the decline.
//
// It copies rather than sorting the caller's slice: a ranking function that
// reorders its input makes every caller's second call depend on its first.
func Rank(resource string, members []Member) []Member {
	scored := make([]scoredMember, len(members))
	for i, m := range members {
		scored[i] = scoredMember{Member: m, score: Score(resource, m.ID)}
	}
	slices.SortFunc(scored, func(a, b scoredMember) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	ranked := make([]Member, len(scored))
	for i, s := range scored {
		ranked[i] = s.Member
	}
	return ranked
}

type scoredMember struct {
	Member
	score uint64
}

// Desired returns the ids of the top `target` accepting members for a resource: the
// deterministic owner set. Fewer than `target` come back when the fleet is smaller
// or enough members declined; whether that is an error is the caller's call.
//
// A duplicate ID in the input is returned once, so a view listing one peer twice
// does not become two replicas.
func Desired(resource string, members []Member, target int) []string {
	if target <= 0 {
		return nil
	}
	out := make([]string, 0, target)
	for _, m := range Rank(resource, members) {
		if !m.Accepts || slices.Contains(out, m.ID) {
			continue
		}
		out = append(out, m.ID)
		if len(out) == target {
			break
		}
	}
	return out
}

// Holds reports whether one member is in the desired set: "is this mine?"
func Holds(resource string, members []Member, target int, id string) bool {
	for _, m := range Desired(resource, members, target) {
		if m == id {
			return true
		}
	}
	return false
}
