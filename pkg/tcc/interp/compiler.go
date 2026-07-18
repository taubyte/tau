// Package interp is the generic tcc interpreter: the compile/decompile entry and
// the schema-driven transform drivers (CompileDriver, ResolveRefs, AttachAll,
// IndexDriver and their decompile inverses), parameterized by an injected schema
// root and project. It deliberately does NOT import the schema package — the
// schema imports THIS package for its capability terms (Cap) and index/group
// annotations, so the dependency is strictly one-way. The public compile/decompile
// API is re-exposed as a thin facade in pkg/tcc/taubyte/v1/schema.
package interp

import (
	"context"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp/pass3"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// Object is an alias for the compiled configuration object.
type Object = object.Object[object.Refrence]

// NextValidation is an alias for a single external validation request.
type NextValidation = engine.NextValidation

type Compiler struct {
	seerOptions []yaseer.Option
	branch      string
	cloud       string
	compileRoot *engine.Node
	engine      engine.Engine
}

var DefaultBranch = "main"

// New builds a Compiler bound to a schema project and its CompileRoot node. The
// project and compileRoot are supplied by the caller (the schema facade passes
// schema.TaubyteProject + schema.CompileRoot()) rather than referenced here, so
// this package never imports schema — the crux that keeps the dependency one-way.
func New(project engine.Schema, compileRoot *engine.Node, options ...Option) (c *Compiler, err error) {
	c = &Compiler{
		branch:      DefaultBranch,
		compileRoot: compileRoot,
	}

	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}

	c.engine, err = engine.New(project, c.seerOptions...)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Compiler) Compile(ctx context.Context) (Object, []NextValidation, error) {
	obj, err := c.engine.Process()
	if err != nil {
		return nil, nil, err
	}

	// The CompileDriver replaces the whole pass1 layer: one generic transform
	// that interprets the schema DSL to do every structural projection pass1 did.
	compileDriver := newCompileDriver(c.compileRoot, c.cloud, c.branch)

	// ResolveRefs replaces the whole pass2 layer: a generic transform that
	// resolves every Ref(...)-annotated attribute against the name->id index the
	// CompileDriver populated (source validation moved to the load-time Validator).
	resolveRefs := ResolveRefs(c.compileRoot)

	// AttachAll performs the AttachesToAll() cross-cutting attachment: it reads the
	// frozen attachSmartOpsFromTags step off the DSL annotation, turning each
	// "smartops:<name>" resource tag into an entry on that resource's `smartops`
	// wire list. Runs after the CompileDriver populated the name->id index.
	attachAll := AttachAll(c.compileRoot)

	// IndexDriver replaces the whole pass4 layer: one generic transform that
	// interprets the DSL's per-resource index-footprint closures to build the
	// compiled `indexes` subtree (explicit V1 order, project + per-app scope).
	indexDriver := NewIndexDriver(c.compileRoot, c.branch)

	pipe := []transform.Transformer[object.Refrence]{}
	for _, p := range [][]transform.Transformer[object.Refrence]{{compileDriver}, {resolveRefs}, {attachAll}, pass3.Pipe(), {indexDriver}} {
		pipe = append(pipe, p...)
	}

	transformCtx := transform.NewContext[object.Refrence](ctx)
	result, err := transform.Pipe(
		transformCtx,
		obj,
		pipe...,
	)
	if err != nil {
		return nil, nil, err
	}

	// Collect validations from transform context store
	validationsStore := transformCtx.Store().Validators()
	validations := validationsStore.Get()

	return result, validations, nil
}
