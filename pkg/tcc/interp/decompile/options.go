package decompile

import (
	"github.com/spf13/afero"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

func WithLocal(path string) Option {
	return func(d *Decompiler) error {
		d.seerOptions = append(d.seerOptions, yaseer.SystemFS(path))
		d.files = afero.NewBasePathFs(afero.NewOsFs(), path)
		return nil
	}
}

func WithVirtual(fs afero.Fs, path string) Option {
	return func(d *Decompiler) error {
		d.seerOptions = append(d.seerOptions, yaseer.VirtualFS(fs, path))
		d.files = afero.NewBasePathFs(fs, path)
		return nil
	}
}

// WithAsLowered writes ALL THE WAY: the resources a lowering produced and the access
// entries it granted stay, so what is written back is the configuration AS LOWERED —
// as it runs — rather than as an author wrote it.
//
// It is not the default and should not become one. What comes out of it does not
// round-trip — compiling it lowers those resources a second time — so it is for a reader
// (a console showing what a workflow became, a diff against a deployed object), never for
// a repository.
func WithAsLowered() Option {
	return func(d *Decompiler) error {
		d.asLowered = true
		return nil
	}
}
