package interp

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Option func(c *Compiler) error

// Env is the compile environment: named compile-time parameters that DSL
// declarations reference by name. interp itself consumes only "branch" (the
// IndexDriver bakes it into index paths); every other key is owned by the schema
// that reads it — e.g. the taubyte schema's WithCloud sets "cloud", consumed by a
// PromoteEnvKeyed declaration, not by interp.
type Env map[string]string

func WithLocal(path string) Option {
	return func(c *Compiler) error {
		c.seerOptions = append(c.seerOptions, yaseer.SystemFS(path))
		return nil
	}
}

func WithVirtual(fs afero.Fs, path string) Option {
	return func(c *Compiler) error {
		c.seerOptions = append(c.seerOptions, yaseer.VirtualFS(fs, path))
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

// WithBranch pins the compile branch, which the IndexDriver bakes into index paths.
func WithBranch(branch string) Option { return WithEnv("branch", branch) }
