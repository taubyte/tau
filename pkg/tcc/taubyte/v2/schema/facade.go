package schema

import (
	"context"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
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
	// The layout goes in because a lowering produces resources, and where a resource lives
	// is this vocabulary's answer rather than the interpreter's. What an access entry is
	// called needs no option: the schema already marks both of its parts, and the lowering
	// reads them there.
	return interp.New(TaubyteProject, CompileRoot(), append(opts, interp.WithLayout(layout()))...)
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

// Became is what one resource became when it compiled: the ids produced from it, and the
// access entries its lowering added to resources somebody wrote. A question about the
// compiled object, because lowering IS compiling.
func Became(obj map[string]any, id string) ([]string, []interp.Granted) {
	return interp.LoweredOf(obj).Of(id)
}

// layout is where this vocabulary keeps its documents, in the interpreter's terms.
func layout() interp.Layout {
	l := LayoutOf()
	return interp.Layout{Resource: l.Resource, Scope: l.Scope}
}

// LoweringPlan is what this project's lowered resources become — the resources, and
// the wiring between them, so a consumer can show what is INSIDE one of them. The
// expansion is tcc's (interp.LoweringPlan); this binds it to THIS vocabulary's root.
func LoweringPlan(obj map[string]any) []interp.Derived {
	return interp.LoweringPlan(CompileRoot(), obj)
}

// Carriages is every authored field this vocabulary's lowerings carry out whole, and
// Remainder is what a shell of that kind has to keep because nothing gives it back. Both
// are tcc's (interp); this binds them to THIS vocabulary's root.
func Carriages(kind string) []interp.Carriage { return interp.Carriages(CompileRoot(), kind) }
func Remainder(kind string) []string          { return interp.Remainder(CompileRoot(), kind) }

// ReadBack recovers the authored values a lowering carried away, out of the resources it
// produced — the half of the round trip a shell that stored only its remainder needs.
func ReadBack(plan []interp.Derived, cs []interp.Carriage) map[string]string {
	return interp.ReadBack(plan, cs)
}

func WithLocal(path string) Option                { return interp.WithLocal(path) }
func WithVirtual(fs afero.Fs, path string) Option { return interp.WithVirtual(fs, path) }

// Mount and WithMounted are the two halves of reading a resource's delegated block
// from the repository its content comes from: Compiler.Mounts says what is needed,
// and this answers one. See pkg/tcc/interp/mount.go for why it is two passes.
type Mount = interp.Mount

func WithMounted(m Mount, fs afero.Fs, commit string) Option {
	return interp.WithMounted(m, fs, commit)
}

// Mounter answers those mounts as the compile runs, which is the same two passes
// with the loop on tcc's side of the call. Most callers want this one; Mounts and
// WithMounted are for a caller that needs the fetches in its own hands.
type Mounter = interp.Mounter

// MountedKey is where a compiled object records the delegated blocks its
// compile read, beside the object itself.
const MountedKey = interp.MountedKey

func WithMounter(m Mounter) Option {
	return interp.WithMounter(m)
}
func WithBranch(branch string) Option   { return interp.WithBranch(branch) }
func WithEnvironment(env string) Option { return interp.WithEnv(EnvironmentVar, env) }

// DecompileToVirtual and WithAsLowered are the decompiler's own options, re-exported so a
// caller depends on this package alone. WithAsLowered is the DEPTH: without it a
// decompile writes back only what somebody wrote, which is what a repository must hold.
func DecompileToVirtual(fs afero.Fs, path string) decompile.Option {
	return decompile.WithVirtual(fs, path)
}
func WithAsLowered() decompile.Option { return decompile.WithAsLowered() }

func NewDecompiler(o ...decompile.Option) (*decompile.Decompiler, error) {
	return decompile.New(TaubyteProject, CompileRoot(), o...)
}

// WithEnvironments names every environment this project declares — the closed set of
// suffixes a document may carry. It is not the environment being compiled (that is
// WithEnvironment): one document serves them all, so production's compile has to know
// preview's spelling is a field and not a typo.
func WithEnvironments(names ...string) Option { return interp.WithVariable(EnvironmentVar, names...) }

// InEnvironment is the binding CompileFor and AuthoredFor take: this vocabulary's one
// variable, held at one value. A caller that says nothing compiles the bare keys.
func InEnvironment(name string) engine.Bound { return engine.Bound{EnvironmentVar: name} }
