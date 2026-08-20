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
	// A lowering produces resources and the entries that reach them, and BOTH shapes are
	// this vocabulary's answer rather than the interpreter's: where a resource lives, and
	// what an access entry is called.
	return interp.New(TaubyteProject, CompileRoot(),
		append(opts, interp.WithLayout(layout()), interp.WithAccess(entryShape()))...)
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

// AuditProject compiles and then runs every cross-resource check over the result.
// The resolver answers what tcc cannot see; nil means the stub, which says yes.
func AuditProject(ctx context.Context, r Resolver, opts ...Option) ([]Finding, error) {
	c, err := New(opts...)
	if err != nil {
		return nil, err
	}
	obj, _, err := c.Compile(ctx)
	if err != nil {
		return nil, err
	}
	return Audit(obj.Flat(), r), nil
}

// Became is what one resource became when it compiled: the ids produced from it, and the
// access entries its lowering added to resources somebody wrote. A question about the
// compiled object, because lowering IS compiling.
func Became(obj map[string]any, id string) ([]string, []interp.Granted) {
	return interp.LoweredOf(obj).Of(id)
}

// entryShape is how this vocabulary writes one resource reaching another, in the
// interpreter's terms. Read off AccessEntryShape, which the audit already reads, so the
// two cannot disagree about what an entry is called.
func entryShape() interp.EntryShape {
	a := AccessEntryShape()
	return interp.EntryShape{Ops: a.Ops, Namespace: a.Namespace}
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
func WithBranch(branch string) Option             { return interp.WithBranch(branch) }
func WithEnvironment(env string) Option           { return interp.WithEnv("environment", env) }

// DecompileToVirtual and WithGenerated are the decompiler's own options, re-exported so a
// caller depends on this package alone. WithGenerated is the DEPTH: without it a
// decompile writes back only what somebody wrote, which is what a repository must hold.
func DecompileToVirtual(fs afero.Fs, path string) decompile.Option {
	return decompile.WithVirtual(fs, path)
}
func WithGenerated() decompile.Option { return decompile.WithGenerated() }

func NewDecompiler(o ...decompile.Option) (*decompile.Decompiler, error) {
	// The entry shape goes in for the same reason it goes into the compiler: taking back
	// an op a lowering added means knowing what this vocabulary calls them.
	return decompile.New(TaubyteProject, CompileRoot(), append(o, decompile.WithAccess(entryShape()))...)
}
