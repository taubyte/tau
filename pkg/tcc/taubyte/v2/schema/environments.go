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

// EnvironmentsOf reads the environment table out of a project's own document
// without compiling it: a compile needs the declared names before it can tell an
// override from a typo. One document, no schema walk.
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
		return nil, nil // the compile refuses a project with no table; this read only reports it
	}
	out := make([]Environment, 0, len(names))
	for _, name := range names {
		at := q.Get(name)
		env := Environment{Name: name, Of: of}
		at.Get(branchesKey).Value(&env.Branches)
		if of == "" {
			at.Get(regionsKey).Value(&env.Regions)
			env.Shadows, _ = environmentsIn(at.Get(shadowsKey), name)
		}
		out = append(out, env)
	}

	// Sorted: yaseer's List ranges a map, and an unstable deployment order cannot be
	// diffed.
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

// EnvironmentsIn reads the table out of a compiled object, with or without its
// `object` wrapper, so a consumer holding what the register published can resolve
// a branch without git.
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

// environmentsFrom is environmentsIn over a decoded map; the tests hold the two
// readers to the same shape and order.
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

// stringsIn reads a string list in either shape a compiled object arrives in:
// []string from an in-process compile, []any after the register's JSON.
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
// declaration order. A project with no table returns nothing rather than an
// invented name.
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

// MatchBranch is the glob a `branches` entry is. `*` crosses `/` (unlike
// path.Match), so `["*"]` matches `feature/checkout`; `?` is one character.
// git-check-ref-format forbids `*` and `?` in branch names, so nothing needs
// escaping.
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
		return interp.WithVariable(EnvironmentVar, EnvironmentNames(envs)...)(c)
	}
}
