package decompile

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func WithLocal(path string) Option {
	return func(d *Decompiler) error {
		d.seerOptions = append(d.seerOptions, yaseer.SystemFS(path))
		return nil
	}
}

func WithVirtual(fs afero.Fs, path string) Option {
	return func(d *Decompiler) error {
		d.seerOptions = append(d.seerOptions, yaseer.VirtualFS(fs, path))
		return nil
	}
}
