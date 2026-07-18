package compiler

import (
	"context"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/driver"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/pass3"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
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
	engine      engine.Engine
}

var DefaultBranch = "main"

func New(options ...Option) (c *Compiler, err error) {
	c = &Compiler{
		branch: DefaultBranch,
	}

	for _, option := range options {
		if err := option(c); err != nil {
			return nil, err
		}
	}

	c.engine, err = engine.New(schema.TaubyteProject, c.seerOptions...)
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
	compileDriver := driver.New(schema.CompileRoot(), c.cloud, c.branch)

	// ResolveRefs replaces the whole pass2 layer: a generic transform that
	// resolves every Ref(...)-annotated attribute against the name->id index the
	// CompileDriver populated (source validation moved to the load-time Validator).
	resolveRefs := driver.ResolveRefs(schema.CompileRoot())

	// AttachAll performs the AttachesToAll() cross-cutting attachment: it reads the
	// frozen attachSmartOpsFromTags step off the DSL annotation, turning each
	// "smartops:<name>" resource tag into an entry on that resource's `smartops`
	// wire list. Runs after the CompileDriver populated the name->id index.
	attachAll := driver.AttachAll(schema.CompileRoot())

	// IndexDriver replaces the whole pass4 layer: one generic transform that
	// interprets the DSL's per-resource index-footprint closures to build the
	// compiled `indexes` subtree (explicit V1 order, project + per-app scope).
	indexDriver := driver.NewIndexDriver(schema.CompileRoot(), c.branch)

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
