package compiler

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Option func(c *Compiler) error

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

func WithBranch(branch string) Option {
	return func(c *Compiler) error {
		c.branch = branch
		return nil
	}
}

// WithCloud pins the compile to a cloud FQDN. pass1.Cloud uses it to
// promote the matching `clouds.<fqdn>.{account, plan}` entry to flat
// scalars and drop the rest. Empty fqdn = no-op.
func WithCloud(fqdn string) Option {
	return func(c *Compiler) error {
		c.cloud = fqdn
		return nil
	}
}
