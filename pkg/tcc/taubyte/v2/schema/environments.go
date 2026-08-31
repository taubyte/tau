package schema

import (
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
