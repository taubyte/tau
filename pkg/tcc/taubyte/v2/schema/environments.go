package schema

import (
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// Environment is one deployment of a project, as the project declares it.
type Environment struct {
	// Name is the key it was written under.
	Name string
	// Branches feeding it, as globs. One branch may feed several deployments.
	Branches []string
	// Regions its state is kept in, empty when the cloud decides. Never set on a
	// shadow: the state is not a shadow's to place.
	Regions []string
	// Shadows is what this environment carries: deployments of their own, over its
	// state and its triggers.
	Shadows []Environment
	// Of is the environment a shadow shadows, and "" for one that stands alone. Not
	// authored — it is where the shadow was written.
	Of string
}

// EnvironmentsOf reads the environment table out of a project's own document, without
// compiling it.
//
// A COMPILE NEEDS THE TABLE BEFORE IT CAN READ THE PROJECT, which is why this exists
// separately: the loader has to know every declared name to tell an override from a
// typo, and the names are in the document it is about to read. So the table is read
// first, from the one place it lives, by the one query that reaches it.
//
// Cheap on purpose: one document, no schema walk, no lowering. It is called once per
// compile and once per push by whatever resolves a branch to its deployments.
func EnvironmentsOf(fs afero.Fs, path string) ([]Environment, error) {
	s, err := yaseer.New(yaseer.VirtualFS(fs, path))
	if err != nil {
		return nil, err
	}
	// The PROJECT's own document, which is what Leaf("project") names — not
	// LayoutOf().Leaf, which is the document a SCOPE keeps its fields in.
	return environmentsIn(s.Query().Get(engine.LeafOf(CompileRoot())).Get(environmentsKey), "")
}

const (
	environmentsKey = "environments"
	shadowsKey      = "shadows"
	branchesKey     = "branches"
	regionsKey      = "regions"
)

func environmentsIn(q *yaseer.Query, of string) ([]Environment, error) {
	names, err := q.List()
	if err != nil {
		return nil, nil // a project that declares none is a project with one deployment
	}
	out := make([]Environment, 0, len(names))
	for _, name := range names {
		at := q.Get(name)
		env := Environment{Name: name, Of: of}
		_ = at.Get(branchesKey).Value(&env.Branches)
		if of == "" {
			_ = at.Get(regionsKey).Value(&env.Regions)
			env.Shadows, _ = environmentsIn(at.Get(shadowsKey), name)
		}
		out = append(out, env)
	}

	// SORTED, BECAUSE THE SOURCE ORDER IS A GO MAP'S. yaseer's List ranges a
	// map, so reading the same document twice yields the same set in a
	// different order — and a deploy that publishes production, then preview,
	// then production-first next time is one whose output nobody can diff and
	// whose failures land on a different deployment each run. The document's
	// own order is not recoverable here, so name order is what there is.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EnvironmentNames is every deployment a project declares, shadows included — the
// closed set of suffixes a document may carry.
func EnvironmentNames(envs []Environment) []string {
	var out []string
	for _, e := range envs {
		out = append(out, e.Name)
		for _, s := range e.Shadows {
			out = append(out, s.Name)
		}
	}
	return out
}

// EnvironmentsIn reads the table out of a COMPILED object.
//
// THE TABLE SURVIVES THE COMPILE, which is what lets a consumer that holds only
// what the register published resolve a branch without going back to git. A
// build triggered by a push to a CODE repository needs exactly that: it knows
// the project and the branch, and everything else it needs is already in the
// object the deploy left behind.
//
// It takes the object with or without its `object` wrapper, the same way
// ReachableNames does, because a caller holding one rarely knows which it has.
func EnvironmentsIn(object map[string]any) []Environment {
	root := object
	if inner, ok := object[interp.ProjectKey].(map[string]any); ok {
		root = inner
	}

	table, ok := root[environmentsKey].(map[string]any)
	if !ok {
		return nil
	}
	return environmentsFrom(table, "")
}

// environmentsFrom is environmentsIn over a decoded map rather than a yaseer
// query. The two readers are deliberately separate — one reads a document
// before a compile and one reads what the compile produced, and neither is a
// special case of the other — but they answer the same shape, in the same
// order, and the tests hold them to it.
func environmentsFrom(table map[string]any, of string) []Environment {
	out := make([]Environment, 0, len(table))
	for name, raw := range table {
		at, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		env := Environment{Name: name, Of: of}
		env.Branches = stringsIn(at[branchesKey])
		if of == "" {
			env.Regions = stringsIn(at[regionsKey])
			if shadows, ok := at[shadowsKey].(map[string]any); ok {
				env.Shadows = environmentsFrom(shadows, name)
			}
		}
		out = append(out, env)
	}

	// Sorted for the reason environmentsIn is: the source is a map either way.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// stringsIn reads a list of strings out of a compiled value, in EITHER of the
// two shapes a compiled object arrives in.
//
// A COMPILE IN THIS PROCESS YIELDS []string; THE SAME OBJECT THROUGH THE
// REGISTER YIELDS []any, because it went through JSON on the way. This read
// handled only the second, and returned nil for the first — silently, since a
// table with no branches and no regions is indistinguishable from one nobody
// wrote.
//
// What that cost: Project.Environments() answered with empty Branches for an
// in-process compile, so Deployments(branch) matched NOTHING and a push
// resolved to no deployment at all; and empty Regions, so every consumer
// reading an environment's allowance read no allowance.
func stringsIn(v any) []string {
	switch list := v.(type) {
	case []string:
		return append([]string(nil), list...)
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// Deployments is every deployment a pushed branch feeds, shadows included, in
// declaration order.
//
// THIS IS THE FUNCTION EnvironmentsOf's DOC PROMISES — "called once per push by
// whatever resolves a branch to its deployments" — and until it existed a push
// had nowhere to send its build and a publish had to be told an environment by
// hand.
//
// ONE BRANCH FEEDS SEVERAL. `main` feeds production AND the `ab` shadow written
// inside it, because a shadow is its own release over its parent's state; a
// caller that took the first match would silently stop deploying one of them.
//
// A project that declares no table declares one deployment, and this returns
// nothing for it: there is no name to return, and inventing one here would be
// this package naming a deployment the author did not.
func Deployments(envs []Environment, branch string) []Environment {
	var out []Environment
	for _, e := range envs {
		if matchesAny(e.Branches, branch) {
			out = append(out, e)
		}
		for _, s := range e.Shadows {
			if matchesAny(s.Branches, branch) {
				out = append(out, s)
			}
		}
	}
	return out
}

// DeploymentNames is Deployments reduced to what a publish addresses.
func DeploymentNames(envs []Environment, branch string) []string {
	var out []string
	for _, e := range Deployments(envs, branch) {
		out = append(out, e.Name)
	}
	return out
}

func matchesAny(globs []string, branch string) bool {
	for _, g := range globs {
		if MatchBranch(g, branch) {
			return true
		}
	}
	return false
}

// MatchBranch is the glob a `branches` entry is.
//
// `*` CROSSES `/`, AND THAT IS THE WHOLE REASON THIS IS NOT path.Match. The
// fixture's preview environment is `branches: ["*"]` and means every branch,
// including `feature/checkout` — under path.Match's separator-aware `*` it
// would match none of the branches anybody actually pushes, and `release/*`
// would stop at `release/1.0` while missing `release/1/0`. Git's own refspec
// globbing crosses separators for the same reason.
//
// `?` is one character. Nothing else is special, and nothing needs escaping:
// git-check-ref-format already forbids `*` and `?` in a branch name, so a glob
// character in a `branches` entry is always a glob character.
func MatchBranch(glob, branch string) bool {
	if glob == "" {
		return false
	}
	var pattern strings.Builder
	pattern.WriteString("^")
	for _, r := range glob {
		switch r {
		case '*':
			pattern.WriteString(".*")
		case '?':
			pattern.WriteString(".")
		default:
			pattern.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	pattern.WriteString("$")

	re, err := regexp.Compile(pattern.String())
	if err != nil {
		return false
	}
	return re.MatchString(branch)
}

// WithProjectEnvironments reads the table off the project at `fs`/`path` and declares
// it, so a caller says where the project is once instead of listing its environments.
func WithProjectEnvironments(fs afero.Fs, path string) Option {
	return func(c *interp.Compiler) error {
		envs, err := EnvironmentsOf(fs, path)
		if err != nil {
			return err
		}
		return interp.WithEnvironments(EnvironmentNames(envs)...)(c)
	}
}
