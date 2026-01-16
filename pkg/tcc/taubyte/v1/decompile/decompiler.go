package decompile

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/decompile/pass1"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/decompile/pass2"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/decompile/pass3"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	"github.com/taubyte/tau/pkg/tcc/transform"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// Object is an alias for the compiled configuration object.
type Object = object.Object[object.Refrence]

type Decompiler struct {
	seerOptions []yaseer.Option
	engine      engine.Engine
}

type Option func(d *Decompiler) error

func New(options ...Option) (d *Decompiler, err error) {
	d = &Decompiler{}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	d.engine, err = engine.New(schema.TaubyteProject, d.seerOptions...)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Decompile converts a compiled object back to YAML files using the engine's schema.
// Note: This modifies the input object in place (same as regular compilation transforms).
func (d *Decompiler) Decompile(obj Object) error {
	// Reverse pipeline: pass1 -> pass2 -> pass3
	pipe := []transform.Transformer[object.Refrence]{}
	for _, p := range [][]transform.Transformer[object.Refrence]{
		pass1.Pipe(),
		pass2.Pipe(),
		pass3.Pipe(),
	} {
		pipe = append(pipe, p...)
	}

	ctx := transform.NewContext[object.Refrence](context.Background())
	restored, err := transform.Pipe(ctx, obj, pipe...)
	if err != nil {
		return fmt.Errorf("reverse pipeline failed: %w", err)
	}

	// Use engine's Dump to write object back to filesystem using schema (syncs automatically)
	return d.engine.Dump(restored)
}
