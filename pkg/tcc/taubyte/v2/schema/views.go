package schema

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// One view per region, cut from the object as it runs (compiled, lowered,
// placed). Each held resource has a Role, from its namespace's topology. What has
// no footprint follows what it reaches; a zone nothing ties down is in every view.
// Where something serves, what it reaches serves too: an edge to a resource held
// only in another region, or only standing by in this one, is Broken, never stubbed
// or dropped — except an origin, which a request is carried to and the view names
// as Remote. Every served region gets a view, even empty; asking for an unserved
// one is an error.

// Role is how a resource is held in one region.
type Role string

const (
	// RoleAnywhere: no scope above it names a region, so it runs wherever the build
	// places it.
	RoleAnywhere Role = "anywhere"
	// RoleSingle: its namespace runs in this one region.
	RoleSingle Role = "single"
	// RoleActive: its namespace runs here and in its other regions at once.
	RoleActive Role = "active"
	// RolePrimary and RoleStandby: its namespace fails over — it serves from the
	// primary, and a standby takes over, in order, when the one before is gone.
	RolePrimary Role = "primary"
	RoleStandby Role = "standby"
	// RolePlaced: the build put it here although its namespace named elsewhere — a
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

// Serves is whether a resource held in this role answers here now. A standby holds
// what it needs to take over, and answers nothing until it does.
func (r Role) Serves() bool { return r != RoleStandby }

// View is one region's projection of a compiled object.
type View struct {
	Region string
	// Object is the projection: the compiled object holding only this region's
	// resources and the scopes they sit in, ids unchanged.
	Object map[string]any
	// Roles is how each resource the view holds is in this region, by compiled id.
	Roles map[string]Role
	// Remote is every origin reached from here and held only elsewhere, by compiled
	// id: the regions it is held in, in its namespace's order.
	Remote map[string][]string
}

// Empty reports whether the view holds no resource at all — a region this build
// serves and the project places nothing in.
func (v View) Empty() bool { return len(v.Roles) == 0 }

// ErrUnknownDestination is a view asked for in a region the Regions does not serve.
var ErrUnknownDestination = errors.New("schema: no view for a region this build does not serve")

// ErrUnviewable is a project that cannot be cut into views: a Broken finding of the
// audit, or an edge across regions.
var ErrUnviewable = errors.New("schema: this project cannot be cut into one view per region")

// Views is every view of a compiled object, one per served region, with the
// audit's findings and every edge refused for crossing regions. It wraps
// ErrUnviewable when any finding is Broken; findings are returned either way.
func Views(obj map[string]any, opts ...AuditOption) (map[string]View, []Finding, error) {
	plan := PlanViews(obj, opts...)
	for _, f := range Audit(obj, StubResolver(), opts...) {
		if !slices.Contains(plan.Findings, f) {
			plan.Findings = append(plan.Findings, f)
		}
	}
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
		out[region] = View{Region: region, Object: projected, Roles: all, Remote: plan.Remote[region]}
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
	// Remote is, per destination, every origin reached from there and held only
	// elsewhere, with the regions it is held in.
	Remote map[string]map[string][]string
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

	plan := Plan{Roles: map[string]map[string]Role{}, Remote: map[string]map[string][]string{}}
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
				roles[at.res.id] = roleOf(at.placement, region)
			}
		}
	}

	needs := map[string][]string{}
	origins := map[string]bool{}
	for _, d := range declaredRelations(all) {
		by, of := d.By(), d.Of()
		if by != of && !slices.Contains(needs[by], of) {
			needs[by] = append(needs[by], of)
		}
		if d.origin {
			origins[by+"\x00"+of] = true
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
	refuse := func(from, to compiled, region, title, detail string) {
		key := from.id + "\x00" + to.id + "\x00" + region
		if refused[key] {
			return
		}
		refused[key] = true
		plan.Findings = append(plan.Findings, Finding{Broken, from.kind, from.name, from.scope, title, detail, from.id})
	}
	notHere := func(from, to compiled, region string) {
		refuse(from, to, region, fmt.Sprintf("reaches %s/%s, which does not run in %q where this does", to.kind, to.name, region),
			"A resource runs where its namespace does, and nothing reaches across regions but a gateway's route to its handler. Run both namespaces where they meet, or move one into the other's.")
	}
	standing := func(from, to compiled, region string) {
		refuse(from, to, region, fmt.Sprintf("reaches %s/%s, which only stands by in %q where this serves", to.kind, to.name, region),
			"A namespace that fails over serves from its first region alone. What reaches it from another of its regions is answered from elsewhere. Fail both over in the same order, or make the one reached active.")
	}

	// DEPENDED ON: what has no footprint and something placed reaches, through what has
	// none either. It is held where what reaches it is, and nowhere else: a package goes
	// with the workloads that run it, and a secret a pinned function reads stays inside
	// the pin.
	depended := map[string]bool{}
	for id := range hasFootprint {
		for _, next := range closureUnplaced(needs, hasFootprint, id) {
			depended[next] = true
		}
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
					role, held := roles[next]
					switch {
					case !held && origins[cur+"\x00"+next]:
						if plan.Remote[region] == nil {
							plan.Remote[region] = map[string][]string{}
						}
						plan.Remote[region][next] = answers[next].answer.In
					case !held:
						notHere(byID[cur], byID[next], region)
					case !role.Serves() && hasFootprint[cur] && roles[cur].Serves():
						standing(byID[cur], byID[next], region)
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

		// FOLLOWS: what has no footprint, is reached by nothing placed, and reaches
		// nothing placed outside this region. A zone nothing points at lands in every
		// view, which is where a resource that runs nowhere of its own is served.
		for _, res := range all {
			if hasFootprint[res.id] || depended[res.id] || roles[res.id] != "" || isScopeOnly(res) {
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
	// What is depended on is held with what depends on it, which is that one's to answer.
	for _, res := range all {
		if hasFootprint[res.id] || depended[res.id] || isScopeOnly(res) {
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
			anywhere := at.placement.In == nil
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
func roleOf(p Placement, region string) Role {
	if !slices.Contains(p.In, region) {
		if p.Topology == "" && p.In == nil {
			return RoleAnywhere
		}
		return RolePlaced
	}
	switch p.Topology {
	case TopologySingle:
		return RoleSingle
	case TopologyActive:
		return RoleActive
	case TopologyFailover:
		if p.In[0] == region {
			return RolePrimary
		}
		return RoleStandby
	}
	return RoleAnywhere
}
