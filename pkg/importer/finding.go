// Package importer is what every importer shares: how it says what it could not
// bring across.
//
// The types are here rather than copied per source because the FINDINGS are the
// product. A caller shows them, a corpus run counts them, and a field appearing
// in three importers' blocked lists is how the missing vocabulary gets
// prioritised. Three private copies of this type would make that impossible.
//
// # The two rules every importer holds
//
// AN IMPORTER NEVER EMITS A FIELD THE RUNTIME CANNOT HONOUR — not as a comment,
// not as "advisory". Config carrying a setting into a system that ignores it is
// worse than a refusal line, because the next person to read it believes it.
//
// EVERY BLOCKED FIELD NAMES THE CONTRACT THAT WOULD UNBLOCK IT. A bare
// NO_TAU_FEATURE is a shrug; naming the field makes it a to-do item.
package importer

import "sort"

// Level says how much a finding costs the person importing.
type Level string

const (
	// Blocked: the source says something tau cannot express at all. The import
	// still produces config, and that config does less than the source did.
	Blocked Level = "blocked"

	// Approximated: it was carried across, but it does not mean exactly the
	// same thing. These are the ones that produce a bug report six weeks later.
	Approximated Level = "approximated"

	// Ignored: a key meaningless here rather than missing — a source concern
	// with no runtime consequence for tau.
	Ignored Level = "ignored"

	// Elsewhere: the value is real and is NOT IN THIS FILE. It lives in the
	// source platform's API, behind credentials.
	//
	// IT IS ITS OWN LEVEL BECAUSE IT IS NOT A GAP IN TAU. Filing it as Blocked
	// would put it on the list of things to build, and there is nothing to
	// build: the fix is an account connection, not a vocabulary. Vercel, Fly,
	// Dokploy and Coolify all keep environment variables, secrets and domains
	// outside their manifests, which is the whole reason a file-only import of
	// those is partial rather than wrong.
	Elsewhere Level = "elsewhere"
)

// Finding is one thing an import did not do faithfully.
type Finding struct {
	// Resource is what it came from — a compose service, a Vercel function
	// glob — or "" for a top-level key.
	Resource string

	// Key is the source's own key, e.g. "healthcheck" or "crons".
	Key string

	Level Level

	// Code is the machine-readable reason, e.g. "NO_TAU_FEATURE:healthcheck"
	// or "SOURCE_WRITE_ONLY:secret". A corpus run counts these.
	Code string

	// Needs names the vocabulary that would unblock it, so the finding is a
	// to-do item rather than a shrug. Empty when nothing in tau would — which
	// is the case for a refusal and for anything Elsewhere.
	Needs string

	// Detail is what to tell the person importing.
	Detail string
}

func (f Finding) String() string {
	s := f.Code
	if f.Resource != "" {
		s = f.Resource + ": " + s
	}
	if f.Needs != "" {
		return s + " — " + f.Detail + " (needs " + f.Needs + ")"
	}
	return s + " — " + f.Detail
}

// Result is what an import produced.
type Result struct {
	// Source names where it came from: "compose", "vercel", "fly".
	Source string

	// Kind is the v2 kind the resources were imported as, when they are all one
	// kind. Empty when an importer produces several.
	Kind string

	// Resources is the imported configuration, by name.
	Resources map[string]map[string]any

	// Findings is everything not carried faithfully.
	Findings []Finding
}

// Add records a finding.
func (r *Result) Add(f Finding) { r.Findings = append(r.Findings, f) }

// Set records an imported resource.
func (r *Result) Set(name string, doc map[string]any) {
	if r.Resources == nil {
		r.Resources = map[string]map[string]any{}
	}
	r.Resources[name] = doc
}

// Sort puts the findings in a stable order, so two runs of one importer diff
// cleanly and a corpus report reads the same twice.
func (r *Result) Sort() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		if r.Findings[i].Resource != r.Findings[j].Resource {
			return r.Findings[i].Resource < r.Findings[j].Resource
		}
		return r.Findings[i].Key < r.Findings[j].Key
	})
}

// AtLevel is every finding at one level.
func (r Result) AtLevel(l Level) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Level == l {
			out = append(out, f)
		}
	}
	return out
}

// Blocked is what tau cannot express, which is what a caller shows first.
func (r Result) Blocked() []Finding { return r.AtLevel(Blocked) }

// Elsewhere is what an account connection would supply. It is asked for
// separately because the answer is "connect your account", not "wait for us to
// build something".
func (r Result) Elsewhere() []Finding { return r.AtLevel(Elsewhere) }
