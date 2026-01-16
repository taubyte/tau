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

func (c *Compiler) Compile(ctx context.Context) (object.Object[object.Refrence], error) {
	obj, err := c.engine.Process()
	if err != nil {
		return nil, err
	}

	pipe := []transform.Transformer[object.Refrence]{}
	for _, p := range [][]transform.Transformer[object.Refrence]{pass1.Pipe(), pass2.Pipe(), pass3.Pipe(), pass4.Pipe(c.branch)} {
		pipe = append(pipe, p...)
	}

	return transform.Pipe(
		transform.NewContext[object.Refrence](ctx),
		obj,
		pipe...,
	)
}
