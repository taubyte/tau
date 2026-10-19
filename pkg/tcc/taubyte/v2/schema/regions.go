package schema

import (
	"fmt"
	"slices"
	"sort"
	"strings"
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
}

// DefaultRegion is the one region a build that serves one region places everything in.
const DefaultRegion = "default"

// OneRegion is the Regions of a build that serves one region: DefaultRegion. Every
// resource runs there. A placement or allowance naming any other region is kept — it is
// the author's statement, and true on a cloud that serves it — and compiles with a
// Check saying it is not served here, so it is never silently dropped and never a
// reason the project cannot deploy.
//
// IT IS THE DEFAULT, so an audit nobody configured is never the one that refuses a
// project for regions: a project writing no `regions` anywhere compiles clean, which is
// what "an absent block is anywhere the allowance permits" promises.
func OneRegion() Regions { return oneRegion{} }

type oneRegion struct{}

func (oneRegion) Place(p Placement) Answer {
	placed := Answer{In: []string{DefaultRegion}}
	if other := notDefault(p.Named()); len(other) > 0 {
		placed.Concerns = []Concern{{Check,
			fmt.Sprintf("is placed in %s, which this build does not serve; it runs in %q", quoted(other), DefaultRegion),
			fmt.Sprintf("This build serves one region, %q, and places everything there. The placement is kept for a cloud that serves those regions.", DefaultRegion)}}
	}
	return placed
}

func (oneRegion) Allow(regions []string) []Concern {
	other := notDefault(regions)
	if len(other) == 0 {
		return nil
	}
	return []Concern{{Check,
		fmt.Sprintf("allows %s, which this build does not serve; everything under it runs in %q", quoted(other), DefaultRegion),
		fmt.Sprintf("This build serves one region, %q, and places everything there. The allowance is kept for a cloud that serves those regions.", DefaultRegion)}}
}

func notDefault(regions []string) []string {
	var out []string
	for _, r := range regions {
		if r != DefaultRegion && !slices.Contains(out, r) {
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

// WithRegions is the Regions the audit asks. Omitted, it is OneRegion.
func WithRegions(r Regions) AuditOption {
	return func(a *auditOf) { a.regions = r }
}

// Place is the audit's placement half on its own: what the Regions answers for every
// scope, environment and placed resource of a compiled object, as findings. A deploy
// that does not run the whole audit runs this, so the platform's answer about regions
// is heard wherever a project is published.
func Place(obj map[string]any, opts ...AuditOption) []Finding {
	in := auditOptions(opts)
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
	for _, env := range EnvironmentsIn(root) {
		if len(env.Regions) == 0 {
			continue
		}
		for _, c := range regions.Allow(env.Regions) {
			out = append(out, Finding{c.Severity, "environments", env.Name, "", c.Title, c.Detail, ""})
		}
	}

	placed := make(map[string]bool, len(PlacedKinds()))
	for _, kind := range PlacedKinds() {
		placed[kind] = true
	}
	environment := environmentRegions(root, in.environment)
	for _, res := range all {
		if !placed[res.kind] {
			continue
		}
		answer := regions.Place(placementOf(res, scopes, environment))
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
