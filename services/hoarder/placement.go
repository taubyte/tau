//go:build !ee

package hoarder

import (
	"sort"

	"github.com/cespare/xxhash/v2"
)

// placementDesired returns the deterministic owner set for an instance: the
// top-`target` live members by rendezvous (HRW) score of (instanceHash,member).
// Every node computes the same result from the same membership, so ownership
// needs no auction — a node simply checks whether it is in the returned set.
// placement_ee.go supplies the tagged build's selection.
func placementDesired(instanceHash string, members []string, target int) []string {
	if target <= 0 || len(members) == 0 {
		return nil
	}
	type scored struct {
		id    string
		score uint64
	}
	ranked := make([]scored, len(members))
	for i, m := range members {
		ranked[i] = scored{id: m, score: hrwScore(instanceHash, m)}
	}
	// Highest score wins; deterministic tie-break by peer id.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})
	if target > len(ranked) {
		target = len(ranked)
	}
	out := make([]string, target)
	for i := 0; i < target; i++ {
		out[i] = ranked[i].id
	}
	return out
}

// hrwScore is the rendezvous weight of a (resource, member) pair: a stable hash
// that reshuffles minimally when the member set changes (only ~1/N keys move per
// membership change), which keeps re-homing cheap.
func hrwScore(instanceHash, member string) uint64 {
	var b []byte
	b = append(b, instanceHash...)
	b = append(b, 0)
	b = append(b, member...)
	return xxhash.Sum64(b)
}
