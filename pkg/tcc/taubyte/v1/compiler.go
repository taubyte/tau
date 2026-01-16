package compiler

import (
	"context"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/pass1"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/pass2"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/pass3"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/pass4"
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

	pipe := []transform.Transformer[object.Refrence]{}
	for _, p := range [][]transform.Transformer[object.Refrence]{pass1.Pipe(), pass2.Pipe(), pass3.Pipe(), pass4.Pipe(c.branch)} {
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
