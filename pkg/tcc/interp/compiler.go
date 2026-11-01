// Package interp is the generic tcc interpreter: compile and decompile, and the
// schema-driven transform drivers, parameterized by an injected schema root and
// project. It does not import the schema package, which imports this one, so
// the dependency is one-way; pkg/tcc/taubyte/v2/schema re-exposes the API.
package interp

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Object = object.Object[object.Refrence]

type NextValidation = engine.NextValidation

type Compiler struct {
	seerOptions []yaseer.Option
	env         Env
	// varValues is, per variable, the closed set of values this project's documents may
	// suffix a key with — which is not the same question as what the variables ARE for
	// the compile being run. See WithVariable.
	varValues   map[string][]string
	compileRoot *engine.Node
	engine      engine.Engine

	// Where the config is READ from, kept so a lowered view can be layered over it
	// without the caller having to say twice where the project is. See view.go.
	project  engine.Schema
	baseFs   afero.Fs
	basePath string
	layout   Layout

	// loweredView is the in-memory filesystem the last Compile lowered over — nil until
	// lower() runs, and nil again for a project that declares no lowering. It is the
	// compile's own working copy, kept rather than rebuilt, so a caller asking "what did
	// this actually write" sees the same documents the compile read back, not a second
	// guess at them. See LoweredFS.
	loweredView afero.Fs

	// mounted is every answered Mount, keyed by the mount point tcc asked for. Empty
	// on a project that delegates nothing, which is every project until one says
	// `source.config` — see mount.go.
	mounted map[string]filled

	// mounter answers Mounts during the compile, so a caller makes one call. nil
	// when a caller drives the two passes itself. See mount.go.
	mounter Mounter
	// mountedOnce guards mountAll: the discovery pass must run once, and it is a
	// compile, so re-entering it would not terminate.
	mountedOnce bool

	// from is the commit each answered fill was read at, keyed by mount point. It is
	// what makes "this block came from there" a fact rather than a claim.
	from map[string]string
}

var DefaultBranch = "main"

// New builds a Compiler bound to a schema project and its CompileRoot node,
// supplied by the caller so this package never imports schema.
func New(project engine.Schema, compileRoot *engine.Node, options ...Option) (c *Compiler, err error) {
	c = &Compiler{
		env:         Env{"branch": DefaultBranch},
		compileRoot: compileRoot,
		project:     project,
	}
	if compileRoot != nil {
		if cycles := loweringCycles(compileRoot); len(cycles) > 0 {
			return nil, fmt.Errorf("interp: lowerings cycle: %s", strings.Join(cycles, "; "))
		}
	}

	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}

	// Every answered fill is merged before the engine is built, so the loader reads
	// one tree. Merged here only when the caller answered by hand; with a mounter,
	// mountAll merges once, since merging in both places would layer a view over a
	// view.
	if len(c.mounted) > 0 && c.mounter == nil {
		view, from, mErr := c.mountedView()
		if mErr != nil {
			return nil, mErr
		}
		c.seerOptions = []yaseer.Option{yaseer.VirtualFS(view, "/")}
		c.baseFs, c.basePath = view, "/"
		c.from = from
	}

	// The DECLARED values travel with the engine because the loader needs them whichever
	// binding is being read. WHAT is bound is an argument to CompileFor.
	c.engine, err = engine.NewWith(project, c.varValues, c.seerOptions...)
	if err != nil {
		return nil, err
	}

	return c, nil
}

// Compile is the configuration as it runs: some kinds are rewritten at compile
// time, so compiling means lowering.
func (c *Compiler) Compile(ctx context.Context) (Object, []NextValidation, error) {
	return c.CompileFor(ctx, c.bound())
}

// CompileFor is Compile under one BINDING — see AuthoredFor.
func (c *Compiler) CompileFor(ctx context.Context, b engine.Bound) (Object, []NextValidation, error) {
	if err := c.mountAll(ctx); err != nil {
		return nil, nil, err
	}
	obj, validations, err := c.AuthoredFor(ctx, b)
	if err != nil {
		return obj, validations, err
	}
	if UsesLowering(c.compileRoot) {
		obj, validations, err = c.lowerFor(ctx, b, obj, validations)
		if err != nil {
			return obj, validations, err
		}
	}
	// The files the document points at, read in after lowering: lowerFor rebuilds the
	// object from the documents, discarding anything written before it.
	if err := c.inlineFiles(obj); err != nil {
		return obj, validations, err
	}

	// Where each delegated block came from, recorded last so it survives the
	// lowering, so a consumer can say which commit is served from the object alone.
	if err := c.recordMounts(obj); err != nil {
		return nil, nil, err
	}
	return obj, validations, nil
}

// bound is the compile variables' values: the compile environment narrowed to
// the keys the schema root declares as variables, so interp spells no variable
// name.
func (c *Compiler) bound() engine.Bound {
	names := engine.VariablesOf(c.compileRoot)
	if len(names) == 0 || len(c.env) == 0 {
		return nil
	}
	var b engine.Bound
	for _, name := range names {
		value, set := c.env[name]
		if !set || value == "" {
			continue
		}
		if b == nil {
			b = engine.Bound{}
		}
		b[name] = value
	}
	return b
}

// LoweredFS is the in-memory filesystem the last Compile lowered over — every document a
// lowering relocated or produced, exactly as the second pass read it back. nil before a
// Compile runs, and nil after one for a project that declares no lowering: there is
// nothing here Authored did not already show.
func (c *Compiler) LoweredFS() afero.Fs {
	return c.loweredView
}

// Authored is the configuration AS IT WAS WRITTEN, with no lowering applied. It is what
// an editor shows and what a lowering is computed from; everything else wants Compile.
func (c *Compiler) Authored(ctx context.Context) (Object, []NextValidation, error) {
	return c.AuthoredFor(ctx, c.bound())
}

// AuthoredFor is Authored under one binding: the documents are parsed once and
// read once per binding.
func (c *Compiler) AuthoredFor(ctx context.Context, b engine.Bound) (Object, []NextValidation, error) {
	obj, err := c.engine.ProcessFor(b)
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

	validationsStore := transformCtx.Store().Validators()
	validations := validationsStore.Get()

	return result, validations, nil
}

// Validate runs the full compile pipeline only for its diagnostics: load and
// referential-integrity errors as err, plus the deferred external checks the
// caller must still run. It is the one whole-config validator, since the
// generated structs model the compiled shape.
func (c *Compiler) Validate(ctx context.Context) ([]NextValidation, error) {
	_, validations, err := c.Compile(ctx)
	return validations, err
}

// compilePipe assembles the transform pipeline from what the schema root actually
// declares, so a schema that doesn't use a feature doesn't pay for its pass. The
// CompileDriver always runs; the rest are gated on generic predicates over the
// root's DSL annotations.
func compilePipe(root *engine.Node, env Env) []transform.Transformer[object.Refrence] {
	pipe := []transform.Transformer[object.Refrence]{
		newCompileDriver(root),
	}

	if usesRefs(root) {
		pipe = append(pipe, ResolveRefs(root))
	}

	// The chroot wraps the project under `object` so a sibling (the index subtree,
	// the lowering record) does not read as a resource group.
	if UsesIndexing(root) || UsesLowering(root) {
		pipe = append(pipe, chrootEnvelope())
	}
	if UsesIndexing(root) {
		pipe = append(pipe, NewIndexDriver(root, env["branch"]))
	}

	return pipe
}
