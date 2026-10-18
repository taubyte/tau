package database

import (
	"fmt"

	"github.com/taubyte/tau/pkg/placement"
	"github.com/taubyte/tau/pkg/specs"
)

// residency.go turns what an author wrote about WHERE into the region list
// placement.go runs over.
//
// # The region block is placement, and it always was
//
// `region/policy` sits in a section called Placement, its doc says "Placement",
// and `pinned` is documented as the data-residency line. Reading it as ROUTING
// — how a request finds a copy — and placing databases off the environment's
// list instead was a misreading of a vocabulary that says so in plain words.
// The environment's `regions` is still here, as the ALLOWANCE: the ceiling a
// resource is placed within, never the placement itself.
//
// # So there are two statements and one of them wins
//
// A resource that declares nothing is placed across its allowance, which is the
// common case and the one that writes no config. A resource that declares a
// region is placed THERE, and the allowance only gets to refuse it. That
// ordering is the whole rule: the narrower statement decides, the wider one
// bounds.
//
// # What is not here: cdn's adaptation
//
// `cdn` seeds and then follows demand. The seed is below; the following is not
// built, and it is the same shape as the maintenance loop placement.go names —
// something has to observe where callers are, decide, and WRITE the decision,
// because a region set every node recomputes from its own view of demand is a
// region set two nodes disagree about. Until that exists a cdn database is a
// seeded one, which is a real answer rather than a placeholder.

// cdnSeed is how many regions a cdn database starts in before demand has said
// anything about where its callers are.
//
// MORE THAN ONE, because a cdn database seeded in a single region would be
// indistinguishable from a pinned one right up until the first adaptation — and
// the adaptation is the part that does not exist yet. Two is the smallest
// number that makes the policy mean something on the day it is written.
const cdnSeed = 2

// placed is a resource's region block as its author wrote it. Empty policy is a
// resource that wrote no block at all, which the vocabulary spells as "anywhere
// the allowance permits".
type placed struct {
	policy string

	// in is every region the branch names. A resource is in MANY regions — a
	// database pinned to eu and us has copies in both, and the floor is how many
	// in EACH — which is why this is a list and why the vocabulary spells it
	// with the same plural a scope uses for its allowance.
	in      []string
	main    string
	standby string
}

// regions is where this database's copies go: the region list holders() and
// holds() are asked about.
//
// AN EMPTY ALLOWANCE IS NOT AN EMPTY ANSWER. "Absent means every region the
// cloud has", which is a set this service cannot enumerate — so an allowance
// that named nothing narrows nothing, and a resource under it is placed exactly
// where it asked.
func (p placed) regions(at address, allowance []string) ([]string, error) {
	switch p.policy {
	case specs.RegionsPolicyPinned:
		return within(p.policy, p.in, allowance)
	case specs.RegionsPolicyFailover:
		// BOTH, and not main-with-standby-as-fallback. Copies live in each of
		// the two from the moment it is written — a standby that only receives
		// state once the main is gone is a standby with nothing to take over.
		// Which of them SERVES is the routing question this file does not
		// answer.
		return within(p.policy, []string{p.main, p.standby}, allowance)
	case specs.RegionsPolicyCdn:
		if len(p.in) > 0 {
			return within(p.policy, p.in, allowance)
		}
		return seeded(at, allowance), nil
	}
	return placementRegions(allowance), nil
}

// within is the declared regions, refused if the allowance forbids one.
//
// REFUSED AND NOT DROPPED. A pin outside the scope's ceiling is a widening the
// compiler's audit is supposed to have caught, so at runtime it is a broken
// deployment; placing the database in the regions that DID pass would put it
// somewhere its author never wrote, and placing it nowhere would read as a
// fleet too small rather than a config that cannot be served.
func within(policy string, declared, allowance []string) ([]string, error) {
	out := placementRegions(declared)
	if len(allowance) == 0 {
		return out, nil
	}

	permitted := make(map[string]bool, len(allowance))
	for _, region := range allowance {
		permitted[region] = true
	}
	for _, region := range out {
		if !permitted[region] {
			return nil, fmt.Errorf("%w: %s names %s", ErrOutsideAllowance, policy, region)
		}
	}
	return out, nil
}

// seeded is where a cdn database starts when its author named no home.
//
// HRW OVER THE ALLOWANCE, KEYED BY THE DATABASE ITSELF, and deliberately not a
// random pick: every node computes the same seed from what it already knows, so
// the first placement costs no coordination and no written decision at all. It
// spreads like random across many databases — two databases in one allowance
// land on different regions — and unlike random it is reproducible, which is
// the difference between a placement an operator can explain and one they can
// only observe.
//
// It reuses Rank rather than sorting scores here, so regions are ordered by the
// same rule and the same tie-break as nodes.
func seeded(at address, allowance []string) []string {
	regions := placementRegions(allowance)
	if len(regions) <= cdnSeed {
		return regions
	}

	members := make([]placement.Member, 0, len(regions))
	for _, region := range regions {
		members = append(members, placement.Member{ID: region, Accepts: true})
	}

	out := make([]string, 0, cdnSeed)
	for _, member := range placement.Rank(resourceOf(at), members)[:cdnSeed] {
		out = append(out, member.ID)
	}
	return placementRegions(out)
}
