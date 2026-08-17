package schema

import (
	"context"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/interp/decompile"
)

// The thin public facade over the generic interpreter, same shape as v1's. It binds
// the interpreter to THIS vocabulary's project + CompileRoot(); interp never imports
// a schema, which is what keeps the dependency one-way.

type (
	NextValidation = interp.NextValidation
	Option         = interp.Option
	Object         = interp.Object
	Compiler       = interp.Compiler
)

var DefaultBranch = interp.DefaultBranch

func New(opts ...Option) (*interp.Compiler, error) {
	return interp.New(TaubyteProject, CompileRoot(), opts...)
}

// Validate runs the whole compile pipeline for diagnostics only, returning the
// deferred external checks and any load-time or referential-integrity error.
func Validate(ctx context.Context, opts ...Option) ([]NextValidation, error) {
	c, err := New(opts...)
	if err != nil {
		return nil, err
	}
	return c.Validate(ctx)
}

func WithLocal(path string) Option                { return interp.WithLocal(path) }
func WithVirtual(fs afero.Fs, path string) Option { return interp.WithVirtual(fs, path) }
func WithBranch(branch string) Option             { return interp.WithBranch(branch) }
func WithEnvironment(env string) Option           { return interp.WithEnv("environment", env) }
func NewDecompiler(o ...decompile.Option) (*decompile.Decompiler, error) {
	return decompile.New(TaubyteProject, CompileRoot(), o...)
}
