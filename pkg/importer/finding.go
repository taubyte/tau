// Package importer is what every importer shares: how it reports what it could not
// bring across. The findings are the product: a corpus run counts them, and a field
// blocked across importers is how missing vocabulary gets prioritised.
//
// An importer never emits a field the runtime cannot honour, and every blocked field
// names the contract that would unblock it.
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

	// Elsewhere: the value is real and not in this file; it lives behind the source
	// platform's API. Its own level because it is not a gap in tau: the fix is an account
	// connection.
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

// Resource is one thing an import produced: a v2 kind, a name, and the document. The
// kind is per resource: one compose file becomes applications and their volumes.
type Resource struct {
	Kind string
	Name string
	Doc  map[string]any
}

type Result struct {
	// Source names where it came from: "compose", "vercel", "fly".
	Source string

	// Resources is the imported configuration, IN ORDER, so two runs of one
	// importer produce the same project and a corpus diff means something.
	Resources []Resource

	// Findings is everything not carried faithfully.
	Findings []Finding
}

func (r *Result) Add(f Finding) { r.Findings = append(r.Findings, f) }

// Set records an imported resource, replacing one already recorded under the
// same kind and name.
func (r *Result) Set(kind, name string, doc map[string]any) {
	for i, existing := range r.Resources {
		if existing.Kind == kind && existing.Name == name {
			r.Resources[i].Doc = doc
			return
		}
	}
	r.Resources = append(r.Resources, Resource{Kind: kind, Name: name, Doc: doc})
}

// Runs gives a workload the package it runs, filed beside it: `<name>_image` for one
// somebody else built, `<name>_code` for one built here. The workload names it, and
// says nothing about its source or build itself.
func (r *Result) Runs(doc map[string]any, name string, pkg map[string]any) string {
	pkgName := name + "_code"
	if src, ok := pkg["source"].(map[string]any); ok && src["image"] != nil {
		pkgName = name + "_image"
	}
	r.Set("package", pkgName, pkg)
	doc["package"] = map[string]any{"name": pkgName}
	return pkgName
}

// Doc is one imported resource's document, for a caller that knows what it is
// looking for.
func (r Result) Doc(kind, name string) (map[string]any, bool) {
	for _, res := range r.Resources {
		if res.Kind == kind && res.Name == name {
			return res.Doc, true
		}
	}
	return nil, false
}

// Names is every resource of one kind, in the order they were produced.
func (r Result) Names(kind string) []string {
	var out []string
	for _, res := range r.Resources {
		if res.Kind == kind {
			out = append(out, res.Name)
		}
	}
	return out
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
