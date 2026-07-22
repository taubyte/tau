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
	env         Env
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
		env:         Env{"branch": DefaultBranch},
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

	transformCtx := transform.NewContext[object.Refrence](ctx)
	result, err := transform.Pipe(
		transformCtx,
		obj,
		compilePipe(c.compileRoot, c.env)...,
	)
	if err != nil {
		return nil, nil, err
	}

	// Collect validations from transform context store
	validationsStore := transformCtx.Store().Validators()
	validations := validationsStore.Get()

	return result, validations, nil
}

// Validate runs the full compile pipeline (load -> assemble -> resolve refs ->
// index) purely to surface diagnostics, discarding the compiled object. It is the
// only honest whole-config validator: the generated structureSpec structs model
// the COMPILED-WIRE shape (resolved ids, projected keys), so they cannot check
// authored-config or cross-element constraints (e.g. "source is '.' or a library",
// "a function's domains are defined domains"). This entry does — it reports load-
// time validation errors and referential-integrity errors as err, and returns the
// deferred external checks (DNS, project_id) the caller must still run. External
// tools (UIs, agents, the wasm build) call this to validate a config tree without
// building artifacts.
func (c *Compiler) Validate(ctx context.Context) ([]NextValidation, error) {
	_, validations, err := c.Compile(ctx)
	return validations, err
}

// compilePipe assembles the transform pipeline from what the schema root actually
// declares, so a schema that doesn't use a feature doesn't pay for its pass. The
// CompileDriver always runs; the rest are gated on generic predicates over the
// root's DSL annotations. For the v1 schema every predicate is true, so the pipe is
// byte-for-byte the historical fixed sequence
// {compileDriver, resolveRefs, attachAll, chroot, indexDriver}.
func compilePipe(root *engine.Node, env Env) []transform.Transformer[object.Refrence] {
	// The CompileDriver replaces the whole pass1 layer: one generic transform that
	// interprets the schema DSL to do every structural projection pass1 did.
	pipe := []transform.Transformer[object.Refrence]{
		newCompileDriver(root, env),
	}

	// ResolveRefs (pass2): resolve every Ref(...)-annotated attribute against the
	// name->id index the CompileDriver populated. Only needed if the schema has refs.
	if usesRefs(root) {
		pipe = append(pipe, ResolveRefs(root))
	}

	// AttachAll: the AttachesToAll() cross-cutting attachment, turning each
	// "<group>:<name>" resource tag into an entry on that resource's wire list. Only
	// needed if some group declares AttachesToAll.
	if usesAttachesToAll(root) {
		pipe = append(pipe, AttachAll(root))
	}

	// IndexDriver: interpret the DSL's per-resource index-footprint closures to build
	// the compiled `indexes` subtree. The chroot exists solely to make room for that
	// `indexes` sibling, so both are gated together on whether any group declares an
	// index footprint.
	if UsesIndexing(root) {
		pipe = append(pipe, chrootEnvelope())
		pipe = append(pipe, NewIndexDriver(root, env["branch"]))
	}

	return pipe
}
