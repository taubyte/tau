package placement

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func fleet(n int) []Member {
	ms := make([]Member, n)
	for i := range ms {
		ms[i] = Member{ID: fmt.Sprintf("12D3KooW%04d", i), Accepts: true}
	}
	return ms
}

func resources(n int) []string {
	rs := make([]string, n)
	for i := range rs {
		rs[i] = fmt.Sprintf("index/s%03d/bucket-%d", i%256, i)
	}
	return rs
}

func TestScoreIsInjectiveOverSeparators(t *testing.T) {
	// A 0x00 join — v1's — scores these two pairs identically. This one does
	// not, because the resource is length-delimited.
	a := Score("a\x00b", "c")
	b := Score("a", "b\x00c")
	require.NotEqual(t, a, b, "length delimiting is what makes the two inputs distinct")

	// And it is a function: same input, same answer, every time.
	require.Equal(t, Score("index/s042", "peerA"), Score("index/s042", "peerA"))
	require.NotEqual(t, Score("index/s042", "peerA"), Score("index/s043", "peerA"))
	require.NotEqual(t, Score("index/s042", "peerA"), Score("index/s042", "peerB"))
}

func TestDesiredIsIndependentOfInputOrder(t *testing.T) {
	members := fleet(12)
	want := Desired("blocks/bafyfoo", members, 3)
	require.Len(t, want, 3)

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 50; i++ {
		shuffled := make([]Member, len(members))
		copy(shuffled, members)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		require.Equal(t, want, Desired("blocks/bafyfoo", shuffled, 3))
	}
}

func TestRankDoesNotReorderTheCallersSlice(t *testing.T) {
	members := fleet(8)
	before := make([]Member, len(members))
	copy(before, members)

	Rank("index/s001", members)
	Rank("index/s002", members)

	require.Equal(t, before, members, "Rank must copy: a caller's second call must not depend on its first")
}

func TestTiesAreBrokenByIdSoTheOrderIsTotal(t *testing.T) {
	// Two members with the same id score identically; the tie-break is <= on
	// the id, so the sort is total and stable regardless of input order.
	ms := []Member{{ID: "b", Accepts: true}, {ID: "a", Accepts: true}}

	// Construct a real tie by scoring the same id twice.
	dup := []Member{{ID: "peer", Accepts: true}, {ID: "peer", Accepts: true}}
	r := Rank("res", dup)
	require.Equal(t, []Member{{ID: "peer", Accepts: true}, {ID: "peer", Accepts: true}}, r)

	// And the ordinary ordering is the same both ways round.
	require.Equal(t, Rank("res", ms), Rank("res", []Member{ms[1], ms[0]}))
}

func TestADuplicateMemberIsNeverTwoReplicas(t *testing.T) {
	// A membership view that lists one peer twice must not produce a "K=2"
	// ack set that is one node.
	got := Desired("blocks/bafyfoo", []Member{
		{ID: "peerA", Accepts: true},
		{ID: "peerA", Accepts: true},
		{ID: "peerB", Accepts: true},
	}, 2)
	require.Equal(t, 2, len(got))
	require.NotEqual(t, got[0], got[1])
}

func TestADeclineRemovesAMemberAndPromotesTheNext(t *testing.T) {
	members := fleet(10)
	full := Desired("blocks/bafyfoo", members, 3)
	ranked := Rank("blocks/bafyfoo", members)

	// Decline the top-ranked holder.
	declined := make([]Member, len(members))
	copy(declined, members)
	for i := range declined {
		if declined[i].ID == full[0] {
			declined[i].Accepts = false
		}
	}
	got := Desired("blocks/bafyfoo", declined, 3)

	require.NotContains(t, got, full[0], "a declining member is not desired")
	require.Equal(t, []string{full[1], full[2], ranked[3].ID}, got,
		"the slot goes to the next member down the same ranking, and nothing else moves")
}

func TestAMemberCannotDeclineItselfIn(t *testing.T) {
	// Accepts is the ONLY thing a member says, and it only ever subtracts.
	// Flipping every member's Accepts on cannot change the answer, because
	// they were already on; flipping one off can only remove that one.
	members := fleet(9)
	base := Desired("index/s007", members, 3)

	for i := range members {
		one := make([]Member, len(members))
		copy(one, members)
		one[i].Accepts = false
		got := Desired("index/s007", one, 3)
		require.NotContains(t, got, members[i].ID)
		for _, id := range base {
			if id == members[i].ID {
				continue
			}
			require.Contains(t, got, id, "declining %s must not evict %s", members[i].ID, id)
		}
	}
}

func TestEveryoneDecliningYieldsNoOwners(t *testing.T) {
	members := fleet(5)
	for i := range members {
		members[i].Accepts = false
	}
	require.Empty(t, Desired("index/s001", members, 3))
	require.False(t, Holds("index/s001", members, 3, members[0].ID))
}

func TestTargetBeyondTheFleetReturnsTheFleet(t *testing.T) {
	members := fleet(2)
	got := Desired("index/s001", members, 5)
	require.Len(t, got, 2, "Desired does not invent a member to fill a slot")

	require.Nil(t, Desired("index/s001", members, 0))
	require.Nil(t, Desired("index/s001", members, -1))
	require.Empty(t, Desired("index/s001", nil, 3))
}

// TestOneMemberLeavingMovesOnlyWhatItHeld is the reshuffle bound, and it is the
// property a "hash mod N" placement fails outright: there, one departure
// re-homes almost the whole keyspace.
func TestOneMemberLeavingMovesOnlyWhatItHeld(t *testing.T) {
	const target = 3
	members := fleet(12)
	leaving := members[5].ID
	after := append(append([]Member{}, members[:5]...), members[6:]...)

	moved, held := 0, 0
	for _, res := range resources(3000) {
		before := Desired(res, members, target)
		got := Desired(res, after, target)

		if !contains(before, leaving) {
			require.Equal(t, before, got, "%s did not hold %s; its owners must not move", leaving, res)
			continue
		}
		held++
		moved++

		// Exactly one slot changed: everything that was not the departed
		// member is still there, in the same relative order.
		var kept []string
		for _, id := range before {
			if id != leaving {
				kept = append(kept, id)
			}
		}
		require.Equal(t, kept, got[:len(kept)], "the surviving holders must not budge")
		require.Len(t, got, target)
	}

	require.Equal(t, held, moved)
	// With 12 members and target 3, a quarter of resources listed the departed
	// member. The bound that matters is that the other three quarters moved
	// nothing at all.
	require.Less(t, moved, 3000, "a departure must not re-home the whole keyspace")
	require.Greater(t, moved, 0)
}

func TestOneMemberJoiningTakesAtMostOneSlotPerResource(t *testing.T) {
	const target = 3
	members := fleet(12)
	joined := append(append([]Member{}, members...), Member{ID: "12D3KooW9999", Accepts: true})

	for _, res := range resources(2000) {
		before := Desired(res, members, target)
		got := Desired(res, joined, target)
		if reflect.DeepEqual(before, got) {
			continue
		}
		require.Contains(t, got, "12D3KooW9999", "the only reason a set changes on a join is the joiner")
		diff := 0
		for _, id := range before {
			if !contains(got, id) {
				diff++
			}
		}
		require.Equal(t, 1, diff, "a join evicts exactly one holder")
	}
}

func TestPlacementSpreadsOverTheFleet(t *testing.T) {
	// Not a uniformity proof — a smoke test that the hash is not degenerate,
	// because a Score that ignored the member would silently pass every other
	// test in this file except the reshuffle ones.
	const target = 3
	members := fleet(10)
	count := map[string]int{}
	for _, res := range resources(3000) {
		for _, id := range Desired(res, members, target) {
			count[id]++
		}
	}
	require.Len(t, count, 10, "every member must own something")
	for id, n := range count {
		require.Greater(t, n, 3000*target/10/3, "%s is starved: %d", id, n)
		require.Less(t, n, 3000*target/10*3, "%s is a hotspot: %d", id, n)
	}
}

func TestTwoResourceNamespacesArePlacedIndependently(t *testing.T) {
	// The reason a bucket can be larger than one node: the index placement and
	// the byte placement are two HRW computations over one member set, and
	// they must not correlate.
	const target = 3
	members := fleet(9)
	same := 0
	for i := 0; i < 2000; i++ {
		idx := Desired(fmt.Sprintf("index/s%03d", i%256), members, target)
		blk := Desired(fmt.Sprintf("blocks/bafy%d", i), members, target)
		if reflect.DeepEqual(idx, blk) {
			same++
		}
	}
	require.Less(t, same, 200, "the two placements track each other: %d/2000 identical", same)
}

func TestHoldsAgreesWithDesired(t *testing.T) {
	members := fleet(8)
	for _, res := range resources(200) {
		want := Desired(res, members, 3)
		for _, m := range members {
			require.Equal(t, contains(want, m.ID), Holds(res, members, 3, m.ID))
		}
		require.False(t, Holds(res, members, 3, "someone-else"))
	}
}

// TestMemberCarriesNothingAMemberCouldLieItselfInWith is the census that holds
// the package doc's claim structural rather than aspirational: if a Capacity,
// a Load or a Weight is ever added to Member, this fails and whoever added it
// has to say so here.
func TestMemberCarriesNothingAMemberCouldLieItselfInWith(t *testing.T) {
	tp := reflect.TypeOf(Member{})
	var fields []string
	for i := 0; i < tp.NumField(); i++ {
		fields = append(fields, tp.Field(i).Name)
	}
	sort.Strings(fields)
	require.Equal(t, []string{"Accepts", "ID"}, fields,
		"a member says who it is and that it declines, and nothing else: a self-reported "+
			"capacity scored by HRW lets a node attract other tenants' data by lying")
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
