package decompile

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func WithVirtual(fs afero.Fs, path string) Option {
	return func(d *Decompiler) error {
		d.seerOptions = append(d.seerOptions, yaseer.VirtualFS(fs, path))
		d.files = afero.NewBasePathFs(fs, path)
		return nil
	}
}

// WithAsLowered keeps the resources a lowering produced and the entries it
// granted: the configuration as it runs. Its output does not round-trip, being
// lowered again on compile, so it is for a reader, never a repository.
func WithAsLowered() Option {
	return func(d *Decompiler) error {
		d.asLowered = true
		return nil
	}
}
