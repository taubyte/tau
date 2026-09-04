// Package placement answers "which members own this resource" by computing it
// rather than by agreeing on it.
//
// Rendezvous hashing (HRW): every member is scored against the resource, the
// top `target` win, and every node that holds the same membership list reaches
// the same answer with no auction, no leader, no election and no raft. That is
// the whole package. It is v1's services/hoarder/placement.go in shape, carried
// here because .notes/v2/service-conventions.md §4 names hand-rolled in-service
// placement as the thing not to port — v1's hoarder and patrick each grew their
// own — and because storage, database and cache all want the identical
// computation. v1 measured the alternative: replacing a timed-bidding auction
// with HRW took replication from 617s to ~8s.
//
// # Two properties, and both are why it is worth having
//
// DETERMINISM. Same resource, same member set, same answer, everywhere, always.
// Score is a pure function of two strings. Desired sorts by it with a total
// tie-break, so there is no dependence on map iteration order, on the order the
// caller happened to collect members in, or on anything a node observed
// locally. TestDesiredIsIndependentOfInputOrder is that claim.
//
// MINIMAL RESHUFFLE. When one member leaves, only the resources it held move,
// and each of those moves exactly one slot — the departed member is replaced by
// the next member down the same ranking, and the other target-1 holders do not
// budge. When one member joins, a resource either takes it (dropping its own
// last holder) or is untouched. Nothing else in the fleet re-homes. That is the
// property a naive "hash the resource, mod the member count" gets wrong: it
// reshuffles ~all of the keyspace on every membership change, which is a fleet
// -wide data movement storm every time one node restarts.
//
// # What a member may say about itself, and what it may not
//
// Member.Accepts is a DECLINE, and it is the only thing a member gets to say.
// A member with Accepts false is skipped and the next member in ranking order
// takes its place; a member cannot raise its own score, cannot name a resource
// it wants, and cannot supply a capacity, a weight or a load figure. So a node
// can lie itself OUT of a placement set and can never lie itself IN.
//
// That asymmetry is the only one that is safe when the figure is self-reported
// over a gossip topic, and v1 got it wrong in the shape that matters: its
// heartbeat carries a Capacity field, and anything that scored on it would let
// a node attract other tenants' data by understating how full it is. There is
// no capacity in this package and no hook to put one in.
//
// # What is deliberately not here
//
// A REGION WEIGHT. .notes/v2/storage-brief.md §11 defers regions cdn/failover
// to a region service that does not exist. When it lands, a weight enters as a
// FIELD on Member and a change to score() — weighted rendezvous is
// -weight/ln(h/2^64) ranked descending, which reduces to ranking by h when
// every weight is equal — and no caller of Desired changes shape. Member is a
// struct rather than the bare string v1 ranked precisely so that is an added
// field rather than a new signature. A nil weight hook is not shipped, because
// a seam nothing exercises is a claim rather than a mechanism.
//
// A DECISION ABOUT WHICH RESOURCE STRING TO USE. Callers name their own
// namespaces. storage runs this twice over one member set with two unrelated
// resource strings — one for a shard's index, one for an object's bytes — and
// their independence is what lets a bucket be larger than one node, so this
// package must not decide the string for them.
package placement

import (
	"encoding/binary"
	"sort"

	"github.com/cespare/xxhash/v2"
)

// Member is a peer as placement sees it: an identity, and one bit that can only
// ever remove it. There is no capacity, no load and no weight — see the package
// doc for why the absence is structural rather than an omission, and
// TestMemberCarriesNothingAMemberCouldLieItselfInWith for the census that holds
// it.
type Member struct {
	// ID is the member's identity — a libp2p peer id in every caller so far,
	// but this package only ever hashes and compares it, so it is a string.
	ID string

	// Accepts is false when the member has declined placement: it is full, it
	// is degraded, or it is shutting down. A declining member is skipped and
	// the next in ranking order takes the slot.
	Accepts bool
}

// Score is the rendezvous weight of one (resource, member) pair.
//
// The two strings are length-delimited rather than separated by a byte,
// because a separator is only injective if neither side can contain it. v1
// joined with a 0x00 and its callers happened never to produce one; that is a
// property of the callers, not of the function, and this one holds for any two
// strings — ("a\x00b", "c") and ("a", "b\x00c") score differently here and
// score identically under a 0x00 join. TestScoreIsInjectiveOverSeparators is
// that pair, run.
func Score(resource, member string) uint64 {
	b := make([]byte, 0, 8+len(resource)+len(member))
	b = binary.BigEndian.AppendUint64(b, uint64(len(resource)))
	b = append(b, resource...)
	b = append(b, member...)
	return xxhash.Sum64(b)
}

// Rank returns every member ordered by descending score, ties broken by
// ascending ID so the order is total. Declining members are ranked like any
// other — Rank is the whole ordering, and Desired is what applies the decline.
//
// It copies rather than sorting the caller's slice: a ranking function that
// reorders its input makes every caller's second call depend on its first.
func Rank(resource string, members []Member) []Member {
	ranked := make([]Member, len(members))
	copy(ranked, members)
	sort.Slice(ranked, func(i, j int) bool {
		si, sj := Score(resource, ranked[i].ID), Score(resource, ranked[j].ID)
		if si != sj {
			return si > sj
		}
		return ranked[i].ID < ranked[j].ID
	})
	return ranked
}

// Desired returns the ids of the top `target` accepting members for a
// resource: the deterministic owner set. Fewer than `target` come back when
// the fleet is smaller than the target or when enough members have declined,
// and a caller that treats "fewer than target" as an error is the caller that
// gets to decide that — this function does not invent members to fill a slot.
//
// A duplicate ID in the input is returned once. Callers assemble this list from
// a membership view, and a membership view that lists one peer twice is a bug
// that must not become two "distinct" replicas holding the same bytes.
func Desired(resource string, members []Member, target int) []string {
	if target <= 0 {
		return nil
	}
	out := make([]string, 0, target)
	seen := make(map[string]struct{}, target)
	for _, m := range Rank(resource, members) {
		if !m.Accepts {
			continue
		}
		if _, dup := seen[m.ID]; dup {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m.ID)
		if len(out) == target {
			break
		}
	}
	return out
}

// Holds reports whether one member is in the desired set. It is the question
// every node actually asks — "is this mine?" — and having it here means a
// caller never writes the linear scan slightly differently.
func Holds(resource string, members []Member, target int, id string) bool {
	for _, m := range Desired(resource, members, target) {
		if m == id {
			return true
		}
	}
	return false
}
