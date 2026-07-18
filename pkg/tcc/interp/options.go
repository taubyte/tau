package interp

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Option func(c *Compiler) error

// Env is the compile environment: named compile-time parameters (branch, cloud, …)
// that DSL declarations reference by name — e.g. PromoteEnvKeyed selects a map entry
// by env["cloud"]. Generalizes the old fixed cloud/branch fields into an open bag.
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

// WithEnv sets a compile-environment variable. WithBranch/WithCloud are thin
// wrappers over it; a caller can set any other key a schema's declarations read.
func WithEnv(key, val string) Option {
	return func(c *Compiler) error {
		c.env[key] = val
		return nil
	}
}

func WithBranch(branch string) Option { return WithEnv("branch", branch) }

// WithCloud pins the compile to a cloud FQDN. A PromoteEnvKeyed("clouds", "cloud", …)
// declaration reads env["cloud"] to promote the matching `clouds.<fqdn>` entry to
// flat scalars and drop the rest. Empty fqdn = no promotion (the map is dropped).
func WithCloud(fqdn string) Option { return WithEnv("cloud", fqdn) }
