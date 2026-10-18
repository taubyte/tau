package worker

import "github.com/taubyte/tau/pkg/specs"

// regions.go reads what a resource ASKS about where it runs. It decides
// nothing: which node runs it is placement's answer, made from this and from
// what the nodes report. A worker is already running on some node by the time
// it resolves anything, so the question has to be carried to whoever chooses.

// Regions is a resource's own `regions` block as placement consumes it, and the
// ceiling its deployment is placed within.
type Regions struct {
	// Tiers are the regions asked for, most wanted first: a node in the first
	// tier is taken before any in the second. Nil is a resource that asked for
	// nothing — placed as it always was, NOT bounded by Allowance, which is a
	// property of this reader and not an accident (see RegionsOf).
	Tiers [][]string

	// Allowance is the deployment's ceiling: a tier naming a region outside it
	// is a declaration the scope forbids. Empty narrows nothing.
	Allowance []string
}

// The policies a `regions` block chooses between. They mean the same thing to
// a workload as to a database — where copies are — and differ only in what a
// copy IS: a database's is state, a workload's is an instance serving it.
const (
	regionsPinned   = specs.RegionsPolicyPinned
	regionsFailover = specs.RegionsPolicyFailover
	regionsCDN      = specs.RegionsPolicyCdn
)

// RegionsOf is what one resource asks for.
//
//	pinned    the regions named, all equally
//	failover  main, then standby — the standby is where it runs when main has
//	          no node that will take it, which is what a standby is
//	cdn       the regions named; with none named, anywhere — a workload has no
//	          state to seed, and following demand is not built
//
// A RESOURCE THAT DECLARES NO BLOCK ASKS FOR NOTHING, and its allowance does
// not narrow it. That is narrower than what the vocabulary says a scope's
// `regions` means — "where everything under it may be" — and it is on purpose
// for now: nodes that report no region are the whole of most fleets, and
// bounding every undeclared workload by its project's list would take them all
// off the air. A database, which holds state, IS bounded that way.
func RegionsOf(proj *specs.Project, environment string, r specs.Resource) Regions {
	var tiers [][]string
	declared := specs.ReadRegions(r)
	switch declared.Policy {
	case regionsPinned:
		tiers = tiersOf(declared.In)
	case regionsFailover:
		tiers = tiersOf([]string{declared.Main}, []string{declared.Standby})
	case regionsCDN:
		tiers = tiersOf(declared.In)
	}
	if tiers == nil {
		return Regions{}
	}
	return Regions{Tiers: tiers, Allowance: proj.Allowance(environment)}
}

// tiersOf drops the holes. An empty name is not "anywhere" — it is a hole in a
// declaration, and reading it as anywhere would run a workload outside every
// region its author named.
func tiersOf(lists ...[]string) [][]string {
	var out [][]string
	for _, list := range lists {
		var tier []string
		for _, region := range list {
			if region != "" {
				tier = append(tier, region)
			}
		}
		if len(tier) > 0 {
			out = append(out, tier)
		}
	}
	return out
}
