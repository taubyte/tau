package schema

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// Which regions exist is the implementation's to say, through Regions; the audit
// uses its answer as the one effective placement every check asks about.

// Placement is everything the config states about where one resource runs: what
// its namespace resolves to.
type Placement struct {
	// Topology is how its namespace runs across its regions: TopologySingle,
	// TopologyFailover, TopologyActive, or "" when no scope above it says.
	Topology string
	// In is the regions it runs in: the nearest `regions/in` up its scope chain,
	// narrowed by the environment the audit was told it reads, in the order written.
	// Nil when nothing declares any; empty when the chain and the environment share
	// no region.
	In []string
}

// Concern is something a Regions has to say about a placement or an allowance. The
// audit files it as a Finding against the resource or scope it was asked about.
type Concern struct {
	Severity      Severity
	Title, Detail string
}

// Answer is a Regions' answer for one resource: where it runs, and what it has to say.
// An empty In is a resource with nowhere to run, which the audit reports as Broken.
type Answer struct {
	In       []string
	Concerns []Concern
}

// Regions answers what the platform compiling a project can do with its placement.
type Regions interface {
	// Place answers where a resource runs, given what the config states about it.
	Place(Placement) Answer
	// Allow answers a scope's or an environment's own `regions` line.
	Allow(regions []string) []Concern
	// Serves is every region this build places anything in: the destinations a
	// compiled project is projected onto, one view each, whether or not anything is
	// placed there. A region it does not name is not a destination at all.
	Serves() []string
}

// DefaultRegion is the destination a build with no regions places everything in. It
// is a name for "where this build runs", not a region a cloud declares.
const DefaultRegion = "default"

// WithNoRegions is the Regions of a build with no regions, the OSS build and the
// default: everything is placed in DefaultRegion, and naming any other region is
// Broken.
func WithNoRegions() Regions { return oneRegion{region: DefaultRegion} }

// WithRegion is the Regions of one region of a cloud that has more: a build or a
// harness that is that region. Naming other regions is not an error — they exist,
// this build just is not them. It places as the cloud would and renders only its own
// view, so an edge is judged there alone: what it holds reaching something placed
// only elsewhere is Broken, and what is placed only elsewhere is simply not in it.
func WithRegion(region string) Regions { return asRegion{region: region} }

type asRegion struct{ region string }

func (a asRegion) Serves() []string         { return []string{a.region} }
func (a asRegion) Allow([]string) []Concern { return nil }

// Place answers every region the resource runs in, named or not here: what its
// namespace says, else this region — a resource nothing places runs everywhere,
// which includes here.
func (a asRegion) Place(p Placement) Answer {
	if p.In == nil {
		return Answer{In: []string{a.region}}
	}
	answer := Answer{In: slices.Clone(p.In)}
	if p.Topology == TopologyFailover && slices.Contains(p.In[1:], a.region) {
		answer.Concerns = []Concern{{Check, fmt.Sprintf("stands by in %q, and this build answers from a standby as from a primary", a.region),
			"Nothing moves a failover between regions yet: each region runs the view it is published, so a standby is held and serves like its primary."}}
	}
	return answer
}

type oneRegion struct{ region string }

// Place puts everything in the one region. A scope naming others is refused once, by
// Allow, rather than once per resource under it.
func (o oneRegion) Place(Placement) Answer { return Answer{In: []string{o.region}} }

func (o oneRegion) Serves() []string { return []string{o.region} }

func (o oneRegion) Allow(regions []string) []Concern {
	other := otherThan(o.region, regions)
	if len(other) == 0 {
		return nil
	}
	return []Concern{{Broken,
		fmt.Sprintf("allows %s, which this build does not serve; everything under it runs in %q", quoted(other), o.region),
		fmt.Sprintf("This build serves one region, %q.", o.region)}}
}

func otherThan(region string, regions []string) []string {
	var out []string
	for _, r := range regions {
		if r != region && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func quoted(regions []string) string {
	q := make([]string, len(regions))
	for i, r := range regions {
		q[i] = fmt.Sprintf("%q", r)
	}
	noun := "region "
	if len(regions) > 1 {
		noun = "regions "
	}
	return noun + strings.Join(q, ", ")
}

// WithRegions is the Regions the audit asks. Omitted, it is WithNoRegions.
func WithRegions(r Regions) AuditOption {
	return func(a *auditOf) { a.regions = r }
}

// Place is the audit's placement half on its own: what the Regions answers for every
// scope, environment and placed resource of a compiled object, as findings. A deploy
// that does not run the whole audit runs this, so the platform's answer about regions
// is heard wherever a project is published.
func Place(obj map[string]any, opts ...AuditOption) []Finding {
	in := auditOptions(opts)
	in.lowered = interp.LoweredOf(obj)
	root := projectOf(obj)
	return place(root, collect(root, ""), in)
}

// placementOf is THE effective-placement calculation: what one resource's namespace
// resolves to, within the environment.
func placementOf(res compiled, scopes map[string]scopeAllowance, environment []string) Placement {
	topology, in := resolvedRegions(scopes, res.scope)
	p := Placement{Topology: topology, In: in}
	switch {
	case environment == nil:
	case p.In == nil:
		p.In = environment
	default:
		p.In = narrow(p.In, environment)
	}
	// Narrowed to one region, whatever it was written as runs there alone.
	if p.Topology != "" && len(p.In) == 1 {
		p.Topology = TopologySingle
	}
	return p
}

// place asks the Regions about every allowance and every placed resource.
func place(root map[string]any, all []compiled, in auditOf) []Finding {
	regions := in.regions
	scopes := scopeAllowances(root)
	var out []Finding

	paths := make([]string, 0, len(scopes))
	for path := range scopes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		s := scopes[path]
		if len(s.regions) == 0 {
			continue
		}
		kind, name := s.kind, s.name
		if path == "" {
			kind, name = "project", stringAt(root, "name")
		}
		for _, c := range regions.Allow(s.regions) {
			out = append(out, Finding{c.Severity, kind, name, s.parent, c.Title, c.Detail, s.id})
		}
	}
	// ONLY THE ENVIRONMENT BEING PLACED, when the audit is told one: a deploy of
	// preview is not refused for what production's line says. Told none, every
	// environment's line is asked, which is the whole-project audit.
	for _, env := range EnvironmentsIn(root) {
		if len(env.Regions) == 0 || (in.environment != "" && !placesIn(env, in.environment)) {
			continue
		}
		for _, c := range regions.Allow(env.Regions) {
			out = append(out, Finding{c.Severity, "environments", env.Name, "", c.Title, c.Detail, ""})
		}
	}

	for _, at := range placementsOf(root, all, in) {
		res, answer := at.res, at.answer
		for _, c := range answer.Concerns {
			out = append(out, Finding{c.Severity, res.kind, res.name, res.scope, c.Title, c.Detail, res.id})
		}
		if len(answer.In) == 0 {
			out = append(out, Finding{Broken, res.kind, res.name, res.scope,
				"is not placed anywhere",
				"Anything with a footprint runs somewhere, and the regions this build serves place it nowhere. Give it regions this build serves, or give a scope above it some.", res.id})
		}
	}
	return out
}

// placed is one resource with a footprint, what it states, and what the Regions answered.
type placed struct {
	res       compiled
	placement Placement
	answer    Answer
}

// placementsOf asks the Regions where every placed resource runs, in the order collect
// found them: every resource of a kind with a footprint.
func placementsOf(root map[string]any, all []compiled, in auditOf) []placed {
	kinds := make(map[string]bool, len(FootprintKinds()))
	for _, kind := range FootprintKinds() {
		kinds[kind] = true
	}
	scopes := scopeAllowances(root)
	environment := environmentRegions(root, in.environment)
	var out []placed
	for _, res := range all {
		if !kinds[res.kind] {
			continue
		}
		p := placementOf(res, scopes, environment)
		out = append(out, placed{res: res, placement: p, answer: in.regions.Place(p)})
	}
	return out
}

// placesIn is whether deploying the named environment keeps state under env: env
// itself, or a shadow of it, which is bounded by the environment it shadows.
func placesIn(env Environment, name string) bool {
	if env.Name == name {
		return true
	}
	for _, shadow := range env.Shadows {
		if shadow.Name == name {
			return true
		}
	}
	return false
}

// environmentRegions is the regions the named environment keeps its state in, nil when
// it names none or no environment was named. A shadow keeps no state of its own, so it
// is bounded by the environment it was written inside.
func environmentRegions(root map[string]any, environment string) []string {
	if environment == "" {
		return nil
	}
	var allowed []string
	for _, env := range EnvironmentsIn(root) {
		if env.Name == environment {
			return env.Regions
		}
		for _, shadow := range env.Shadows {
			if shadow.Name == environment {
				allowed = env.Regions
			}
		}
	}
	return allowed
}

func stringAt(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// Preview is the Regions of no build: how a project would be cut across the named
// regions, for tooling. It never refuses; a region outside the list is a Check. A
// build never uses it.
func Preview(regions ...string) Regions {
	p := preview{}
	for _, r := range regions {
		if r != "" && !slices.Contains(p.regions, r) {
			p.regions = append(p.regions, r)
		}
	}
	return p
}

type preview struct{ regions []string }

func (p preview) Serves() []string { return slices.Clone(p.regions) }

func (p preview) Place(pl Placement) Answer {
	from := p.regions
	if pl.In != nil {
		from = pl.In
	}
	var a Answer
	for _, r := range from {
		if slices.Contains(p.regions, r) && !slices.Contains(a.In, r) {
			a.In = append(a.In, r)
		}
	}
	return a
}

func (p preview) Allow(regions []string) []Concern {
	if other := p.outside(regions); len(other) > 0 {
		return []Concern{{Check, fmt.Sprintf("allows %s, which this preview does not show", quoted(other)), p.shows()}}
	}
	return nil
}

func (p preview) outside(regions []string) []string {
	var out []string
	for _, r := range regions {
		if !slices.Contains(p.regions, r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func (p preview) shows() string {
	if len(p.regions) == 0 {
		return "This preview shows no regions."
	}
	return "This preview shows " + strings.Join(p.regions, ", ") + "."
}

// RegionsNamed is every region a compiled project names anywhere — a scope's or an
// environment's — sorted: what a preview offers to show when nobody has said which
// regions a cloud has.
func RegionsNamed(obj map[string]any) []string {
	root := projectOf(obj)
	seen := map[string]bool{}
	for _, s := range scopeAllowances(root) {
		for _, r := range s.regions {
			seen[r] = true
		}
	}
	for _, env := range EnvironmentsIn(root) {
		for _, r := range env.Regions {
			seen[r] = true
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		if r != "" {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}
