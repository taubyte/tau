package database

import (
	"sort"

	"github.com/taubyte/tau/pkg/placement"
)

// placement.go answers which nodes hold a database, and it answers it PER
// REGION.
//
// # Regions multiply the floor, they do not divide it
//
// An environment declares "Regions its state is kept in". That is the pin: a
// database in an environment pinned to eu and us has copies in BOTH, and the
// operator's floor is how many in EACH. Two regions at a floor of two is four
// nodes, not two spread over the pair — a fleet that lost a region would
// otherwise lose the database, which is the failure the pin exists to survive.
//
// SO IT IS ONE HRW PER REGION, over that region's members only. Every node
// still computes the same answer from the same inputs, and a region's placement
// is independent of every other region's: losing a node in eu moves an eu
// replica and does not touch us.
//
// # What is NOT here: policy
//
// `region/policy` — auto, cdn, failover — is ROUTING. It says how a request
// finds a copy: only here, by caller location, or main-then-standby. Where the
// copies are is this file, and the two are independent on purpose — a failover
// policy over a database pinned to one region is a routing rule with one
// destination, and neither has to know about the other.
//
// # An unpinned environment is the cloud's choice
//
// Empty regions means the cloud decides, which here means one placement over
// every member regardless of where it is. That is the single-region case and
// the development case, and it is not a special path: it is the map with one
// entry whose key nobody named.

// anyRegion is the bucket members fall in when an environment names no regions.
// It is a key and never a region name, so a cloud that later pins the
// environment moves the database rather than finding it already "in" a region
// nobody declared.
const anyRegion = ""

// resourceOf is what a kv database is placed by.
//
// THE ID AND NOT THE LABEL, for the reason everything else in this service uses
// the id: a label is local to whoever wrote it, and two databases called
// `tokens` in two projects must not land on the same nodes by virtue of sharing
// a word.
//
// THE MODEL IS IN THE STRING because the four models are placed independently:
// a project's kv and its vector index have no reason to share a replica set,
// and naming them apart means growing one does not move the other.
//
// THE REGION IS NOT IN IT. Placement runs over one region's members at a time,
// so the region is which member set is asked and not part of what is asked
// about — putting it in the string as well would make a database's eu replicas
// and its us replicas different resources that happen to share a name.
func resourceOf(at address) string {
	return "kv/" + at.project + "/" + at.environment + "/" + at.storeName()
}

// holders is every node that should hold this database, by region.
//
// The member set is grouped by the region each node reports. A region the
// environment pinned that no member serves comes back EMPTY rather than being
// filled from somewhere else — a database that cannot be kept where it was
// pinned is a refusal an operator has to see, not a copy quietly placed outside
// its residency line.
func holders(at address, byRegion map[string][]placement.Member, regions []string, replication int) map[string][]string {
	out := make(map[string][]string, len(regions))
	for _, region := range placementRegions(regions) {
		out[region] = placement.Desired(resourceOf(at), byRegion[region], replication)
	}
	return out
}

// holds reports whether one node should hold this database.
//
// IT IS THE WHOLE MAINTENANCE LOOP IN ONE CALL. A node asks it of every
// database it currently holds — false means drop, because something else took
// the slot — and of every database it does not, where true means acquire. Both
// directions fall out of the same function, which is why a membership change
// needs no coordination: every node reaches the same conclusion about itself.
//
// A NODE ONLY EVER COMPETES IN ITS OWN REGION. Asking about a node in eu for a
// database pinned to us is false without consulting anything, which is the
// residency line holding at the one place it has to.
func holds(at address, byRegion map[string][]placement.Member, regions []string, replication int, id, region string) bool {
	for _, pinned := range placementRegions(regions) {
		if pinned != region {
			continue
		}
		return placement.Holds(resourceOf(at), byRegion[pinned], replication, id)
	}
	return false
}

// placementRegions is where copies go: what the environment pinned, or the one
// unnamed bucket when it pinned nothing.
//
// SORTED, so two nodes computing the same placement iterate the same way and a
// caller reading the result gets a stable order. Desired is already independent
// of input order; this is about what comes out.
func placementRegions(regions []string) []string {
	if len(regions) == 0 {
		return []string{anyRegion}
	}

	out := make([]string, 0, len(regions))
	seen := make(map[string]bool, len(regions))
	for _, region := range regions {
		if region == "" || seen[region] {
			// An empty name in a pinned list is not the unnamed bucket — it is
			// a hole in the declaration, and treating it as "anywhere" would
			// place a copy outside every region the author named.
			continue
		}
		seen[region] = true
		out = append(out, region)
	}
	if len(out) == 0 {
		return []string{anyRegion}
	}
	sort.Strings(out)
	return out
}

// copies is how many nodes hold this database in total: the floor, once per
// pinned region. It is what an operator is billed for and what a test asserts.
func copies(regions []string, replication int) int {
	return len(placementRegions(regions)) * replication
}
