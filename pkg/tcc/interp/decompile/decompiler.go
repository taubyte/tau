package decompile

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

type Object = object.Object[object.Refrence]

type Decompiler struct {
	seerOptions []yaseer.Option
	compileRoot *engine.Node
	engine      engine.Engine

	// asLowered is the all-the-way depth: write the configuration as it RUNS rather than
	// as an author wrote it. See WithAsLowered and lowered.go.
	asLowered bool

	// files is where the documents are written, rooted there, for the files a
	// document only names -- see writeInlined. Rooted so that a path in an object
	// handed to us from outside cannot write beside the tree it was asked for.
	files afero.Fs
}

type Option func(d *Decompiler) error

// New builds a Decompiler bound to a schema project and its CompileRoot node,
// supplied by the caller so this package never imports schema.
func New(project engine.Schema, compileRoot *engine.Node, options ...Option) (d *Decompiler, err error) {
	d = &Decompiler{compileRoot: compileRoot}

	for _, option := range options {
		if err := option(d); err != nil {
			return nil, err
		}
	}
	if d.asLowered && interp.UsesLoweringScopes(compileRoot) {
		d.compileRoot = interp.LoweredRoot(compileRoot)
		project = engine.SchemaDefinition(d.compileRoot)
	}

	d.engine, err = engine.New(project, d.seerOptions...)
	if err != nil {
		return nil, err
	}

	return d, nil
}

// Decompile writes a compiled object back as YAML files: what somebody wrote,
// with the lowering's resources and grants taken out first (lowered.go). It
// modifies obj in place.
func (d *Decompiler) Decompile(obj Object) error {
	// The record sits BESIDE the project, so it has to be read before the unwrap keeps
	// the project and drops everything else.
	//
	// The inlined files are read off the compiled shape for the same reason.
	inlined := interp.InlinedFiles(d.compileRoot, obj)
	ctx := transform.NewContext[object.Refrence](context.Background())
	restored, err := transform.Pipe(ctx, obj, d.pipe(loweredOf(obj))...)
	if err != nil {
		return fmt.Errorf("reverse pipeline failed: %w", err)
	}

	if err := d.engine.Dump(restored); err != nil {
		return err
	}
	return d.writeInlined(inlined)
}

// writeInlined puts back the files the compile read INTO the object. The document keeps
// only the path (the driver drops the key the content was read into), so without this a
// decompiled zone names a file that is not there and the next compile reads nothing.
func (d *Decompiler) writeInlined(files map[string][]string) error {
	if d.files == nil {
		return nil
	}
	for path, lines := range files {
		body := strings.Join(lines, "\n")
		if len(lines) > 0 {
			body += "\n"
		}
		if err := d.files.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if err := afero.WriteFile(d.files, path, []byte(body), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}
	return nil
}

// pipe is the reverse pipeline: chroot unwrap (only if the forward path
// chrooted), lowering removal, then DecompileDriver. The removal runs before the
// driver, which keys every resource by name while the record speaks in ids.
func (d *Decompiler) pipe(lowered interp.Lowered) []transform.Transformer[object.Refrence] {
	pipe := []transform.Transformer[object.Refrence]{}
	if interp.UsesIndexing(d.compileRoot) || interp.UsesLowering(d.compileRoot) {
		pipe = append(pipe, unwrapEnvelope())
	}
	pipe = append(pipe, &authoredOnly{lowered: lowered, keep: d.asLowered})
	return append(pipe, interp.NewDecompileDriver(d.compileRoot))
}
