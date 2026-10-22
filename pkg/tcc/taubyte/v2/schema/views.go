package schema

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// ONE VIEW PER REGION: the compiled project as the place it runs in holds it.
//
// A region receives what runs there and nothing it would have to filter out on every
// read. The cut is taken here, once, from the object as it RUNS — compiled, lowered and
// placed by the Regions the audit asks — and tcc's Project does the structural half.
// What this file decides is the vocabulary's half: which resources a region holds, and
// in what ROLE.
//
// A ROLE, NOT JUST MEMBERSHIP. Being in a region means different things by policy, and
// a view that only listed members would erase the difference: a failover database is in
// two regions and serves from one of them; a cdn is seeded in some and arrives in the
// rest on demand. So every resource a view holds says how it is there (Role).
//
// WHAT HAS NO FOOTPRINT FOLLOWS WHAT IT REACHES. A gateway, a zone, a secret runs
// nowhere of its own (PlacedKinds says which kinds do), so it is held where something
// placed needs it, and wherever everything placed it reaches already is: a gateway in
// front of a function pinned to eu is in eu's view, and a zone nothing ties to a place
// is in every view.
//
// AN EDGE ACROSS REGIONS IS REFUSED. A resource held in one region that reaches a
// placed resource held only in another would resolve to nothing in its own view, and
// there is no contract for reaching across: no protocol, no route. So it is Broken,
// named, before anything is projected — never a stub, and never a silent drop. A kind
// that can be reached remotely would be a statement on the kind, and none makes one.
//
// AN EMPTY VIEW IS NOT AN UNKNOWN ONE. A region the Regions serves gets a view even when
// nothing is placed there, so a resource REMOVED from a region is the region's view
// losing it rather than the region falling silent; a region it does not serve has no
// view at all, and asking for one is an error.

// Role is how a resource is held in one region.
type Role string

const (
	// RoleAnywhere: it wrote no placement, and this region is within its allowance.
	RoleAnywhere Role = "anywhere"
	// RolePinned: pinned here.
	RolePinned Role = "pinned"
	// RoleMain and RoleStandby: a failover's two halves — it serves from main, and
	// standby takes over when main is gone.
	RoleMain    Role = "main"
	RoleStandby Role = "standby"
	// RoleSeed: a cdn starts here. RoleDemand: it may arrive here when demand moves it.
	RoleSeed   Role = "seed"
	RoleDemand Role = "demand"
	// RolePlaced: the build put it here although its placement named elsewhere — a
	// build serving fewer regions than the project was written for.
	RolePlaced Role = "placed"
	// RoleNeeded: it has no footprint, and something held here reaches it.
	RoleNeeded Role = "needed"
	// RoleFollows: it has no footprint, nothing here reaches it, and everything placed
	// that it reaches is held here.
	RoleFollows Role = "follows"
	// RoleScope: a resource that is also a scope, held here because something inside
	// it is.
	RoleScope Role = "scope"
)

// View is one region's projection of a compiled object.
type View struct {
	Region string
	// Object is the projection: the compiled object holding only this region's
	// resources and the scopes they sit in, ids unchanged.
	Object map[string]any
	// Roles is how each resource the view holds is in this region, by compiled id.
	Roles map[string]Role
}

// Empty reports whether the view holds no resource at all — a region this build
// serves and the project places nothing in.
func (v View) Empty() bool { return len(v.Roles) == 0 }

// ErrUnknownDestination is a view asked for in a region the Regions does not serve.
var ErrUnknownDestination = errors.New("schema: no view for a region this build does not serve")

// ErrUnviewable is a project that cannot be cut into views: a Broken placement finding,
// or an edge across regions.
var ErrUnviewable = errors.New("schema: this project cannot be cut into one view per region")

// Views is every view of a compiled object, keyed by the regions the Regions serves —
// one each, empty or not — with the placement findings and every edge refused for
// crossing regions. It errors, wrapping ErrUnviewable, when any finding is Broken; the
// findings are returned either way, so a caller can say all of them.
func Views(obj map[string]any, opts ...AuditOption) (map[string]View, []Finding, error) {
	plan := PlanViews(obj, opts...)
	for _, f := range plan.Findings {
		if f.Severity == Broken {
			return nil, plan.Findings, fmt.Errorf("%w: %s", ErrUnviewable, f)
		}
	}
	out := make(map[string]View, len(plan.Destinations))
	for _, region := range plan.Destinations {
		roles := plan.Roles[region]
		projected := interp.Project(kindRootNode(), obj, func(id string) bool { _, ok := roles[id]; return ok })
		all := make(map[string]Role, len(roles))
		for id, role := range roles {
			all[id] = role
		}
		// A scope that is a resource and is held for what is inside it says so.
		for _, res := range collect(projectOf(projected), "") {
			if _, ok := all[res.id]; !ok {
				all[res.id] = RoleScope
			}
		}
		out[region] = View{Region: region, Object: projected, Roles: all}
	}
	return out, plan.Findings, nil
}

// ViewIn is one region's view out of Views, or ErrUnknownDestination.
func ViewIn(views map[string]View, region string) (View, error) {
	v, ok := views[region]
	if !ok {
		return View{}, fmt.Errorf("%w: %q", ErrUnknownDestination, region)
	}
	return v, nil
}

// Plan is the vocabulary's half of the views: per destination, what it holds and how.
type Plan struct {
	// Destinations is every region the Regions serves, in its order.
	Destinations []string
	// Roles is, per destination, every resource held there by its own placement or
	// for what it reaches — not the scopes, which the projection keeps on its own.
	Roles map[string]map[string]Role
	// Findings is the placement half of the audit and every edge refused for crossing
	// regions.
	Findings []Finding
}

// PlanViews is Views without the projection: who goes where, and what stops it.
func PlanViews(obj map[string]any, opts ...AuditOption) Plan {
	in := auditOptions(opts)
	in.lowered = interp.LoweredOf(obj)
	root := projectOf(obj)
	all := collect(root, "")

	plan := Plan{Roles: map[string]map[string]Role{}}
	for _, r := range in.regions.Serves() {
		if !slices.Contains(plan.Destinations, r) {
			plan.Destinations = append(plan.Destinations, r)
			plan.Roles[r] = map[string]Role{}
		}
	}
	plan.Findings = place(root, all, in)

	byID := make(map[string]compiled, len(all))
	for _, res := range all {
		byID[res.id] = res
	}

	// PLACED: where the Regions answered, in the role the placement gives it there.
	hasFootprint := map[string]bool{}
	answers := map[string]placed{}
	for _, at := range placementsOf(root, all, in) {
		hasFootprint[at.res.id] = true
		answers[at.res.id] = at
		for _, region := range at.answer.In {
			// A region this build does not render is placed there all the same; it
			// is just not a view here.
			if roles, ok := plan.Roles[region]; ok {
				roles[at.res.id] = roleOf(at.placement, at.answer, region)
			}
		}
	}

	needs := map[string][]string{}
	for _, d := range declaredRelations(all) {
		by, of := d.By(), d.Of()
		if by != of && !slices.Contains(needs[by], of) {
			needs[by] = append(needs[by], of)
		}
	}
	for _, list := range needs {
		sort.Strings(list)
	}

	// reachedPlaced is every placed resource something reaches, through what has no
	// footprint of its own.
	reachedPlaced := func(id string) []string {
		var out []string
		seen := map[string]bool{id: true}
		queue := []string{id}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range needs[cur] {
				if seen[next] {
					continue
				}
				seen[next] = true
				if hasFootprint[next] {
					out = append(out, next)
					continue
				}
				queue = append(queue, next)
			}
		}
		sort.Strings(out)
		return out
	}

	refused := map[string]bool{}
	refuse := func(from, to compiled, region string) {
		key := from.id + "\x00" + to.id + "\x00" + region
		if refused[key] {
			return
		}
		refused[key] = true
		plan.Findings = append(plan.Findings, Finding{Broken, from.kind, from.name, from.scope,
			fmt.Sprintf("reaches %s/%s, which is not placed in %q where this is", to.kind, to.name, region),
			"Every resource is held only in the regions it is placed in, and nothing reaches across regions: there is no contract for it. Place both where they meet, or let the one reached be placed wherever this is.", from.id})
	}

	for _, region := range plan.Destinations {
		roles := plan.Roles[region]

		// NEEDED: whatever has no footprint and something held here reaches, and what
		// that reaches in turn. A placed target reached from here must be here too.
		queue := make([]string, 0, len(roles))
		for id := range roles {
			queue = append(queue, id)
		}
		sort.Strings(queue)
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, next := range needs[cur] {
				if hasFootprint[next] {
					if _, ok := roles[next]; !ok {
						refuse(byID[cur], byID[next], region)
					}
					continue
				}
				if _, ok := roles[next]; ok {
					continue
				}
				roles[next] = RoleNeeded
				queue = append(queue, next)
			}
		}

		// FOLLOWS: what has no footprint, is reached by nothing here, and reaches
		// nothing placed outside this region. A zone nothing points at lands in every
		// view, which is where a resource that runs nowhere of its own is served.
		for _, res := range all {
			if hasFootprint[res.id] || roles[res.id] != "" || isScopeOnly(res) {
				continue
			}
			ok := true
			for _, target := range reachedPlaced(res.id) {
				if _, here := roles[target]; !here {
					ok = false
					break
				}
			}
			if ok {
				roles[res.id] = RoleFollows
				// what it reaches with no footprint follows it here
				for _, next := range closureUnplaced(needs, hasFootprint, res.id) {
					if roles[next] == "" {
						roles[next] = RoleNeeded
					}
				}
			}
		}
		// NEEDED OUTRANKS FOLLOWS: whatever something held here reaches is needed here,
		// whichever of the two the walk came to first.
		for id := range roles {
			for _, next := range needs[id] {
				if roles[next] == RoleFollows {
					roles[next] = RoleNeeded
				}
			}
		}
	}

	// HELD NOWHERE: something with no footprint whose placed targets share no region.
	for _, res := range all {
		if hasFootprint[res.id] || isScopeOnly(res) {
			continue
		}
		held := false
		for _, region := range plan.Destinations {
			if plan.Roles[region][res.id] != "" {
				held = true
				break
			}
		}
		if !held && len(plan.Destinations) > 0 && !meetElsewhere(reachedPlaced(res.id), answers, plan.Destinations) {
			plan.Findings = append(plan.Findings, Finding{Broken, res.kind, res.name, res.scope,
				"reaches resources placed in no one region together",
				"It has no placement of its own, so it is held where what it reaches is; what it reaches shares no region, and nothing reaches across regions.", res.id})
		}
	}

	sort.SliceStable(plan.Findings, func(i, j int) bool {
		rank := map[Severity]int{Broken: 0, Check: 1, Note: 2}
		return rank[plan.Findings[i].Severity] < rank[plan.Findings[j].Severity]
	})
	return plan
}

// meetElsewhere is whether every target is placed in one region this build does not
// render, so what reaches them is held there and not here. A target that says nothing
// runs everywhere and meets any region.
func meetElsewhere(targets []string, answers map[string]placed, rendered []string) bool {
	var candidates []string
	for _, id := range targets {
		for _, r := range answers[id].answer.In {
			if !slices.Contains(rendered, r) && !slices.Contains(candidates, r) {
				candidates = append(candidates, r)
			}
		}
	}
	for _, r := range candidates {
		all := true
		for _, id := range targets {
			at := answers[id]
			anywhere := at.placement.Policy == "" && at.placement.Allowed == nil
			if !anywhere && !slices.Contains(at.answer.In, r) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// isScopeOnly is a scope that is not itself a resource: it is a place, kept by the
// projection for what is inside it, and never held for its own sake.
func isScopeOnly(res compiled) bool { return scopeGroups()[res.kind] && !isResourceKind(res.kind) }

// closureUnplaced is everything with no footprint reachable from id through things with
// none either.
func closureUnplaced(needs map[string][]string, hasFootprint map[string]bool, id string) []string {
	var out []string
	seen := map[string]bool{id: true}
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range needs[cur] {
			if seen[next] || hasFootprint[next] {
				continue
			}
			seen[next] = true
			out = append(out, next)
			queue = append(queue, next)
		}
	}
	return out
}

// roleOf is how a placement is in one of the regions the Regions answered for it.
func roleOf(p Placement, a Answer, region string) Role {
	switch p.Policy {
	case "":
		return RoleAnywhere
	case "pinned":
		if slices.Contains(p.In, region) {
			return RolePinned
		}
	case "failover":
		switch region {
		case p.Main:
			return RoleMain
		case p.Standby:
			return RoleStandby
		}
	case "cdn":
		if slices.Contains(p.In, region) {
			return RoleSeed
		}
		// NO SEED IT NAMED IS SERVED — none written, or none this build has — so the
		// platform seeds the choice, and every region it runs in is a seed.
		for _, r := range a.In {
			if slices.Contains(p.In, r) {
				return RoleDemand
			}
		}
		return RoleSeed
	}
	return RolePlaced
}
