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

// Lowered is the project as it is AFTER lowering: a compiler over an in-memory view that
// holds the documents a lowering produces and the access entries it puts on authored
// resources. The files on disk are not touched. Compile it and the object has the
// queues, the functions and the entries — which is what a runtime needs, because nothing
// runs a workflow.
func Lowered(ctx context.Context, c *Compiler) (*Compiler, error) {
	l := LayoutOf()
	return c.Lowered(ctx, interp.Layout{Resource: l.Resource, Scope: l.Scope})
}

// ViewFsOf is for tests: the filesystem a compiler reads from.
func ViewFsOf(c *Compiler) afero.Fs { return interp.FsOf(c) }

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
func NewDecompiler(o ...decompile.Option) (*decompile.Decompiler, error) {
	return decompile.New(TaubyteProject, CompileRoot(), o...)
}
