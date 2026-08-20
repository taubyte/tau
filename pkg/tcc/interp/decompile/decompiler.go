package decompile

import (
	"context"
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// Object is an alias for the compiled configuration object.
type Object = object.Object[object.Refrence]

type Decompiler struct {
	seerOptions []yaseer.Option
	compileRoot *engine.Node
	engine      engine.Engine

	// asLowered is the all-the-way depth: write the configuration as it RUNS rather than
	// as an author wrote it. See WithAsLowered and lowered.go.
	asLowered bool
}

type Option func(d *Decompiler) error

// New builds a Decompiler bound to a schema project and its CompileRoot node.
// Both are supplied by the caller (the schema facade passes schema.TaubyteProject
// + schema.CompileRoot()) rather than referenced here, so this package never
// imports schema — keeping the interpreter dependency one-way.
func New(project engine.Schema, compileRoot *engine.Node, options ...Option) (d *Decompiler, err error) {
	d = &Decompiler{compileRoot: compileRoot}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}

	d.engine, err = engine.New(project, d.seerOptions...)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Decompile converts a compiled object back to YAML files using the engine's schema.
//
// It writes what somebody WROTE: the resources a lowering produced, and the access
// entries it granted, are taken back out first — see lowered.go, and WithAsLowered for
// the depth that keeps them.
//
// Note: This modifies the input object in place (same as regular compilation transforms),
// so a caller that still needs the compiled object as it was compiles again.
func (d *Decompiler) Decompile(obj Object) error {
	// The record sits BESIDE the project, so it has to be read before the unwrap keeps
	// the project and drops everything else.
	ctx := transform.NewContext[object.Refrence](context.Background())
	restored, err := transform.Pipe(ctx, obj, d.pipe(loweredOf(obj))...)
	if err != nil {
		return fmt.Errorf("reverse pipeline failed: %w", err)
	}

	// Use engine's Dump to write object back to filesystem using schema (syncs automatically)
	return d.engine.Dump(restored)
}

// pipe is the reverse pipeline: chroot unwrap -> lowering removal -> DecompileDriver. The
// generic DecompileDriver is the mechanical inverse of the forward CompileDriver +
// ResolveRefs, driven by the same schema DSL; it replaces the hand-written id->name ref
// resolution and per-resource inverse projection.
//
// The chroot-unwrap is only needed when the forward path chrooted, and it chroots to make
// room for a sibling: the `indexes` subtree, or the record of what was lowered. When it
// did not there is no `object` wrapper and the unwrap is a no-op, so gating it keeps v1
// identical and skips dead work for a schema with neither sibling.
//
// The lowering removal runs BEFORE the driver: the record addresses resources and
// accessors by their compiled id, and the driver's last act is to key every resource by
// its name instead.
func (d *Decompiler) pipe(lowered interp.Lowered) []transform.Transformer[object.Refrence] {
	pipe := []transform.Transformer[object.Refrence]{}
	if interp.UsesIndexing(d.compileRoot) || interp.UsesLowering(d.compileRoot) {
		pipe = append(pipe, unwrapEnvelope())
	}
	pipe = append(pipe, &authoredOnly{lowered: lowered, keep: d.asLowered})
	return append(pipe, interp.NewDecompileDriver(d.compileRoot))
}
