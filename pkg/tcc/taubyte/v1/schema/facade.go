package schema

import (
	"context"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/interp/decompile"
)

// NextValidation is a deferred external check (DNS, project_id) a compile/validate
// surfaces for the caller to run. Re-exported so tools depend only on this package.
type NextValidation = interp.NextValidation

// Validate builds a compiler and runs the whole-config validate pass in one shot:
// it returns the deferred external checks and any load-time or referential-
// integrity error, without producing a compiled artifact. This is the entry
// external tools (UIs, agents) use to validate a config tree.
func Validate(ctx context.Context, opts ...Option) ([]NextValidation, error) {
	c, err := New(opts...)
	if err != nil {
		return nil, err
	}
	return c.Validate(ctx)
}

// This file is the thin public facade over the generic interpreter (pkg/tcc/interp).
// External callers depend only on this schema package: it binds the interpreter's
// compile/decompile entry to THIS schema's project + CompileRoot(), so the DSL and
// its interpreter stay decoupled (interp never imports schema — the crux that keeps
// the dependency one-way). The concrete return types (*interp.Compiler /
// *decompile.Decompiler) are preserved; callers only use their methods.

// ---- compile facade ----

// Option configures a compile. It is interp's option type, re-exported so callers
// pass schema.WithLocal(...) etc. without naming the interp package.
type Option = interp.Option

// Object is the compiled configuration object New(...).Compile returns.
type Object = interp.Object

// Compiler is the bound compiler New(...) returns — re-exported so callers can
// name it (e.g. a helper returning *schema.Compiler) without importing interp.
type Compiler = interp.Compiler

// DefaultBranch is the branch a compile assumes when WithBranch is not given.
var DefaultBranch = interp.DefaultBranch

// New builds a Compiler for this schema, supplying the project + CompileRoot the
// interpreter needs (which it cannot reference itself without an import cycle).
func New(opts ...Option) (*interp.Compiler, error) {
	return interp.New(TaubyteProject, CompileRoot(), opts...)
}

// WithLocal reads the config tree from the host filesystem at path.
func WithLocal(path string) Option { return interp.WithLocal(path) }

// WithVirtual reads the config tree from an afero filesystem rooted at path.
func WithVirtual(fs afero.Fs, path string) Option { return interp.WithVirtual(fs, path) }

// WithBranch pins the compile branch (baked into index paths).
func WithBranch(branch string) Option { return interp.WithBranch(branch) }

// WithCloud pins the compile to a cloud FQDN, promoting its clouds.<fqdn> entry.
// "cloud" is a taubyte domain var: the cloudsGroup() PromoteEnvKeyed declaration
// reads it — interp knows nothing about it, so the key lives here, not in interp.
func WithCloud(fqdn string) Option { return interp.WithEnv("cloud", fqdn) }

// ---- decompile facade ----

// NewDecompiler builds a Decompiler for this schema, supplying the project +
// CompileRoot the interpreter needs.
func NewDecompiler(opts ...decompile.Option) (*decompile.Decompiler, error) {
	return decompile.New(TaubyteProject, CompileRoot(), opts...)
}

// DecompilerWithLocal writes the decompiled config tree to the host filesystem at
// path. Named distinctly from the compile WithLocal (they configure different
// concrete option types).
func DecompilerWithLocal(path string) decompile.Option { return decompile.WithLocal(path) }

// DecompilerWithVirtual writes the decompiled config tree to an afero filesystem
// rooted at path.
func DecompilerWithVirtual(fs afero.Fs, path string) decompile.Option {
	return decompile.WithVirtual(fs, path)
}
