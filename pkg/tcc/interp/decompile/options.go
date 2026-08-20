package decompile

import (
	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
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

// WithAccess tells the decompiler what this vocabulary calls the parts of an access
// entry. Taking back an op a lowering added means knowing which field holds them, and
// that name belongs to the vocabulary — the same shape the compiler is given.
func WithAccess(a interp.EntryShape) Option {
	return func(d *Decompiler) error {
		d.access = a
		return nil
	}
}

// WithGenerated writes ALL THE WAY: the resources a lowering produced and the access
// entries it granted stay, so what is written back is what the compiler produced rather
// than what an author wrote.
//
// It is not the default and should not become one. What comes out of it does not
// round-trip — compiling it lowers the generated resources a second time — so it is for a
// reader (a console showing what a workflow became, a diff against a deployed object),
// never for a repository.
func WithGenerated() Option {
	return func(d *Decompiler) error {
		d.generated = true
		return nil
	}
}
