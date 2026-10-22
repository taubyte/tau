package interp

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Option func(c *Compiler) error

// Env is the compile environment: named compile-time parameters that DSL
// declarations reference by name. interp spells none of these keys: the VARIABLES
// among them are the ones the schema root declares with Varying, which is where the
// compile binding comes from, and every other key is owned by the schema that reads
// it.
type Env map[string]string

func WithLocal(path string) Option {
	return func(c *Compiler) error {
		c.seerOptions = append(c.seerOptions, yaseer.SystemFS(path))
		c.baseFs, c.basePath = afero.NewBasePathFs(afero.OsFs{}, path), ""
		return nil
	}
}

func WithVirtual(fs afero.Fs, path string) Option {
	return func(c *Compiler) error {
		c.seerOptions = append(c.seerOptions, yaseer.VirtualFS(fs, path))
		c.baseFs, c.basePath = fs, path
		return nil
	}
}

// WithEnv sets a compile-environment variable. WithBranch is a thin wrapper over
// it; a schema exposes its own wrappers (e.g. WithCloud) for the keys its own
// declarations read, so interp needs no knowledge of those domain vars.
func WithEnv(key, val string) Option {
	return func(c *Compiler) error {
		c.env[key] = val
		return nil
	}
}

// WithLayout tells the compiler where this vocabulary keeps its documents, which is what
// a lowering needs to write one. A schema that declares lowerings and no layout cannot be
// compiled — loudly, because the alternative is an object silently missing the resources
// a workflow becomes.
func WithLayout(l Layout) Option {
	return func(c *Compiler) error {
		c.layout = l
		return nil
	}
}

// WithBranch pins the compile branch, which the IndexDriver bakes into index paths.
func WithBranch(branch string) Option { return WithEnv("branch", branch) }

// WithVariable names every value one of the schema's variables may take in THIS
// project. One document serves them all, so it carries every value's spelling of a
// field at once and each compile reads one — which means the loader needs the whole set
// to tell an override from a typo, and WithEnv alone cannot supply it.
//
// Repeatable, one call per variable: the values are a property of the project, and the
// variables themselves are the root's to declare.
func WithVariable(name string, values ...string) Option {
	return func(c *Compiler) error {
		if c.varValues == nil {
			c.varValues = map[string][]string{}
		}
		c.varValues[name] = append([]string(nil), values...)
		return nil
	}
}
