package interp

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Option func(c *Compiler) error

// Env is the compile environment, named parameters DSL declarations read.
// interp spells no key: the variables among them are those the root declares
// with Varying, and every other key belongs to the schema reading it.
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

// WithEnv sets a compile-environment variable; a schema wraps its own keys.
func WithEnv(key, val string) Option {
	return func(c *Compiler) error {
		c.env[key] = val
		return nil
	}
}

// WithLayout says where this vocabulary keeps its documents, which a lowering
// needs; a schema with lowerings and no layout refuses to compile.
func WithLayout(l Layout) Option {
	return func(c *Compiler) error {
		c.layout = l
		return nil
	}
}

// WithBranch pins the compile branch, which the IndexDriver bakes into index paths.
func WithBranch(branch string) Option { return WithEnv("branch", branch) }

// WithVariable names every value one of the schema's variables may take in this
// project, which the loader needs to tell an override from a typo. One call per
// variable.
func WithVariable(name string, values ...string) Option {
	return func(c *Compiler) error {
		if c.varValues == nil {
			c.varValues = map[string][]string{}
		}
		c.varValues[name] = append([]string(nil), values...)
		return nil
	}
}
