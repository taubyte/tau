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

	"github.com/spf13/afero"

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

// New builds a Compiler bound to a schema project and its CompileRoot node. The
// project and compileRoot are supplied by the caller (the schema facade passes
// schema.TaubyteProject + schema.CompileRoot()) rather than referenced here, so
// this package never imports schema — the crux that keeps the dependency one-way.
func New(project engine.Schema, compileRoot *engine.Node, options ...Option) (c *Compiler, err error) {
	c = &Compiler{
		env:         Env{"branch": DefaultBranch},
		compileRoot: compileRoot,
		project:     project,
	}

	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}

	// EVERY ANSWERED FILL IS MERGED BEFORE THE ENGINE IS BUILT, so the loader reads
	// one tree and nothing downstream can tell a delegated block from an authored
	// one. The view replaces the filesystem option rather than being layered after
	// it: WithLocal and WithVirtual are the only writers of seerOptions, and two
	// filesystems for one compile is the ambiguity this exists to remove.
	// Merged HERE only when the caller answered by hand; with a mounter, mountAll
	// merges once after it has asked. Merging in both places would layer a view over
	// a view, and a fill would then read as conflicting with itself.
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

// Compile is the configuration AS IT RUNS.
//
// Some kinds are rewritten at compile time — a workflow is not run by anything; what runs
// is the queues and functions it becomes — so compiling means lowering. There is no
// second call to remember: a caller who forgot one would be holding an object that is
// missing the resources it needs, and nothing about it would say so.
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
	// WHERE EACH DELEGATED BLOCK CAME FROM, recorded last so it survives a lowering
	// that rebuilt the object. A consumer answers "which commit is this serving" from
	// the object alone, which is the only thing that keeps the whole visible when part
	// of it was written somewhere else.
	if err := c.recordMounts(obj); err != nil {
		return nil, nil, err
	}
	return obj, validations, nil
}

// bound is what the compile variables ARE for this compiler: the compile-environment
// bag, narrowed to the keys the SCHEMA ROOT declares as variables.
//
// THIS IS WHY interp SPELLS NO VARIABLE NAME. The root already states which of its
// compile parameters are axes, so the binding is an intersection rather than a literal;
// a vocabulary that adds a second variable needs no line here. Everything else in the
// bag — a cloud, a branch — is a compile parameter some declaration reads, not an axis a
// key may be suffixed with.
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

// AuthoredFor is Authored under one BINDING. One parse serves several readings, so the
// binding is an argument: the documents are parsed once by the engine this compiler
// holds and read once per binding, rather than a compiler built per binding re-reading
// the tree each time.
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

	// The chroot wraps the project under `object` so a sibling can sit beside it. Two
	// things want one: the index subtree, and the record of what a compile lowered.
	// Without it that record would be a top-level key among the resource groups, and
	// every walk over the object would read it as a group of resources called `lowered`.
	if UsesIndexing(root) || UsesLowering(root) {
		pipe = append(pipe, chrootEnvelope())
	}
	// IndexDriver: interpret the DSL's per-resource index-footprint closures to build
	// the compiled `indexes` subtree.
	if UsesIndexing(root) {
		pipe = append(pipe, NewIndexDriver(root, env["branch"]))
	}

	return pipe
}
