package schema

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHICH REGIONS EXIST IS NOT THE CONFIG'S TO SAY, and not tcc's either. A region name
// is an opaque label the implementation defines (see regionAttributes), so the audit
// can hold a project to what it states about itself — a scope narrows and never widens,
// failover takes two — but not to what the platform can do with it. That question is
// answered by whoever compiles, through Regions, and the audit uses its answer as the
// effective placement: one calculation, the same one for every check that asks where
// something runs.

// Placement is everything the config states about where one resource runs.
type Placement struct {
	// Policy is the branch the resource's own block took: "pinned", "cdn",
	// "failover", or "" when it wrote no block.
	Policy string
	// In is a pinned or cdn resource's regions.
	In []string
	// Main and Standby are a failover's two regions.
	Main, Standby string
	// Allowed is the ceiling it is placed within: the nearest `regions` up its scope
	// chain, narrowed by the regions of the environment the audit was told it reads.
	// Nil when nothing above it declares one; empty when what is declared above it
	// shares no region with the environment.
	Allowed []string
}

// Named is every region the resource's own block names.
func (p Placement) Named() []string {
	out := slices.Clone(p.In)
	for _, r := range []string{p.Main, p.Standby} {
		if r != "" {
			out = append(out, r)
		}
	}
	return out
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

// WithNoRegions is the Regions of a build that has no regions — the OSS build. Everything
// is placed in DefaultRegion, and a placement or allowance naming any region is Broken:
// the author stated a place this build cannot put it.
//
// IT IS THE DEFAULT, so an audit nobody configured refuses what it cannot honour
// rather than accepting it quietly. A project writing no `regions` anywhere — or
// naming DefaultRegion — compiles clean, which is what "an absent block is anywhere
// the allowance permits" promises.
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

// Place answers every region the resource runs in that this build can name: the ones
// it states, else its ceiling, else this region — a resource saying nothing runs
// everywhere, which includes here.
func (a asRegion) Place(p Placement) Answer {
	here := p.Allowed == nil || slices.Contains(p.Allowed, a.region)
	switch {
	case p.Policy == "cdn":
		in := slices.Clone(p.In)
		if here && !slices.Contains(in, a.region) {
			in = append(in, a.region)
		}
		return Answer{In: in}
	case len(p.Named()) > 0:
		return Answer{In: p.Named()}
	case p.Allowed != nil:
		return Answer{In: slices.Clone(p.Allowed)}
	}
	return Answer{In: []string{a.region}}
}

type oneRegion struct{ region string }

func (o oneRegion) Place(p Placement) Answer {
	placed := Answer{In: []string{o.region}}
	if other := otherThan(o.region, p.Named()); len(other) > 0 {
		placed.Concerns = []Concern{{Broken,
			fmt.Sprintf("is placed in %s, which this build does not serve; it runs in %q", quoted(other), o.region),
			fmt.Sprintf("This build serves one region, %q.", o.region)}}
	}
	return placed
}

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

// placementOf is THE effective-placement calculation: what one resource states, within
// the ceiling above it.
func placementOf(res compiled, scopes map[string]scopeAllowance, environment []string) Placement {
	p := Placement{
		Policy:  res.str("regions/policy"),
		In:      listAt(res.fields, "regions/in"),
		Main:    res.str("regions/main"),
		Standby: res.str("regions/standby"),
		Allowed: effectiveAllowance(scopes, res.scope),
	}
	switch {
	case len(environment) == 0:
	case len(p.Allowed) == 0:
		p.Allowed = environment
	default:
		// Never nil: a chain and an environment that share no region allow
		// nothing, which is not the same statement as declaring no ceiling.
		narrowed := []string{}
		for _, r := range p.Allowed {
			if slices.Contains(environment, r) {
				narrowed = append(narrowed, r)
			}
		}
		p.Allowed = narrowed
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

// placementsOf asks the Regions where every resource with a footprint runs, in the
// order collect found them.
func placementsOf(root map[string]any, all []compiled, in auditOf) []placed {
	kinds := make(map[string]bool, len(PlacedKinds()))
	for _, kind := range PlacedKinds() {
		kinds[kind] = true
	}
	scopes := scopeAllowances(root)
	environment := environmentRegions(root, in.environment)
	byID := make(map[string]compiled, len(all))
	for _, res := range all {
		byID[res.id] = res
	}
	var out []placed
	for _, res := range all {
		if !kinds[res.kind] {
			continue
		}
		p := placementOf(res, scopes, environment)
		// WHAT A SHELL LOWERS TO RUNS WHERE THE SHELL DOES, unless it says otherwise:
		// a workflow pinned to one region is its functions and queues pinned there.
		if from, ok := in.lowered.Resources[res.id]; ok && p.Policy == "" && len(p.Named()) == 0 {
			if shell, ok := byID[from.From]; ok {
				s := placementOf(shell, scopes, environment)
				p.Policy, p.In, p.Main, p.Standby = s.Policy, s.In, s.Main, s.Standby
			}
		}
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

// Preview is the Regions of no build at all: an author's look at how a project would
// be cut across the regions named, for tooling that shows views before any cloud is
// chosen. It places like a cloud that has exactly those regions — what says nothing
// runs wherever its ceiling allows, a cdn wherever it is seeded and may follow demand —
// and it never refuses: a region outside the list is a Check, because a preview has no
// authority to say a region does not exist.
//
// A BUILD NEVER USES IT. Which regions a cloud serves is the cloud's to say, and the
// one that says it is whatever the build composes; this is what an explorer passes
// when the question is "what would each region hold".
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
	var a Answer
	if other := p.outside(pl.Named()); len(other) > 0 {
		a.Concerns = []Concern{{Check, fmt.Sprintf("is placed in %s, which this preview does not show", quoted(other)), p.shows()}}
	}
	from := p.regions
	if pl.Allowed != nil {
		from = pl.Allowed
	}
	switch {
	case pl.Policy == "cdn":
		from = append(slices.Clone(pl.In), from...)
	case len(pl.Named()) > 0:
		from = pl.Named()
	}
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
// environment's allowance, a resource's placement — sorted: what a preview offers to
// show when nobody has said which regions a cloud has.
func RegionsNamed(obj map[string]any) []string {
	root := projectOf(obj)
	seen := map[string]bool{}
	add := func(rs ...string) {
		for _, r := range rs {
			if r != "" {
				seen[r] = true
			}
		}
	}
	for _, s := range scopeAllowances(root) {
		add(s.regions...)
	}
	for _, env := range EnvironmentsIn(root) {
		add(env.Regions...)
	}
	for _, res := range collect(root, "") {
		add(placementOf(res, nil, nil).Named()...)
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
