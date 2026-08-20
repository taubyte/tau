package interp

import (
	"context"
	"os"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// THE LOWERED VIEW: the project as it is after lowering, in memory.
//
// A workflow is not something a runtime runs. What runs is queues and functions — so if
// what a workflow becomes is not in the compiled object, there is nothing to execute and
// the config is a picture of a program rather than a program. Computing the plan on
// demand, which is what this used to do, put those resources everywhere except where
// they were needed.
//
// They are not written to disk, and the authored files are never touched. The view is a
// copy-on-write layer over the project: the documents a lowering produces are added to
// it, and an authored document that gains an access entry is edited IN THE LAYER. What
// comes back is a compiler over that layer, so everything downstream — reference
// resolution, scalars, validation, indexing — happens exactly as it does for config
// somebody wrote. Which is the strongest claim the design can make: what a lowering
// produces is not a special kind of resource, it is config.

// Layout says where a document lives, as templates over `{kind}` and `{name}`. The
// vocabulary owns the answer (it is the same one decompile writes by), so it is passed
// in rather than assumed here.
type Layout struct {
	Resource string // "{kind}/{name}.yaml"
	Scope    string // "namespace/{name}"
}

// Lowered returns a compiler over the lowered view. When the vocabulary declares no
// lowerings — or this project uses none — it returns the compiler it was given, because
// a view identical to the project is a copy nobody needs.
func (c *Compiler) Lowered(ctx context.Context, layout Layout) (*Compiler, error) {
	if !UsesLowering(c.compileRoot) {
		return c, nil
	}
	obj, _, err := c.Compile(ctx)
	if err != nil {
		return nil, err
	}
	plan := LoweringPlan(c.compileRoot, obj.Flat())
	if len(plan) == 0 {
		return c, nil
	}

	base := c.baseFs
	if c.basePath != "" {
		base = afero.NewBasePathFs(base, c.basePath)
	}
	view, err := overlay(base)
	if err != nil {
		return nil, err
	}

	// The documents FIRST, all of them, and only then the entries: an entry can land on
	// a document another lowering produces (an agent's memory is searched by the loop it
	// also produced), and editing a file that is about to be written would be edited and
	// then replaced.
	emissions := Emissions(c.compileRoot, obj.Flat(), layout)
	for _, e := range emissions {
		for _, doc := range e.Documents {
			if err := write(view, doc); err != nil {
				return nil, err
			}
		}
	}

	seer, err := yaseer.New(yaseer.VirtualFS(view, "/"))
	if err != nil {
		return nil, err
	}
	for _, e := range emissions {
		for _, entry := range e.Entries {
			if err := placeEntry(seer, entry); err != nil {
				return nil, err
			}
		}
	}
	if err := seer.Sync(); err != nil {
		return nil, err
	}

	return New(c.project, c.compileRoot, append(c.viewOptions(), WithVirtual(view, "/"))...)
}

// viewOptions is everything about this compiler except where it reads from.
func (c *Compiler) viewOptions() []Option {
	var out []Option
	for k, v := range c.env {
		out = append(out, WithEnv(k, v))
	}
	return out
}

// overlay is a writable layer over a read-only project. Every directory the project has
// is created in the layer first: afero's copy-on-write cannot make a directory that
// exists only in the base, and a lowering writes into directories the author already
// has.
func overlay(base afero.Fs) (afero.Fs, error) {
	layer := afero.NewMemMapFs()
	err := afero.Walk(base, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || !info.IsDir() {
			return nil // a file the walk cannot read is the compile's problem, not this one's
		}
		return layer.MkdirAll(path, 0o755)
	})
	if err != nil {
		return nil, err
	}
	return afero.NewCopyOnWriteFs(afero.NewReadOnlyFs(base), layer), nil
}

func write(fs afero.Fs, doc Document) error {
	if dir := doc.Path[:strings.LastIndex(doc.Path, "/")+1]; dir != "" {
		if err := fs.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return afero.WriteFile(fs, doc.Path, []byte(doc.Body), 0o644)
}

// placeEntry writes one access entry onto the document of the resource that declares it,
// leaving the rest of that document alone — which is what yaseer is for.
func placeEntry(seer *yaseer.Seer, e Entry) error {
	q := seer.Query()
	for _, part := range strings.Split(strings.TrimSuffix(strings.TrimPrefix(e.Path, "/"), ".yaml"), "/") {
		q = q.Get(part)
	}
	entry := q.Document().Get(e.Kind).Get(e.Name)
	if e.Namespace != "" {
		if err := entry.Get("namespace").Set(e.Namespace).Commit(); err != nil {
			return err
		}
	}
	return entry.Get("ops").Set([]string{e.Op}).Commit()
}

// Root is the schema this compiler compiles against, for a caller that needs to ask the
// vocabulary a question about what it just compiled.
func (c *Compiler) Root() *engine.Node { return c.compileRoot }

// FsOf is where a compiler reads from. Exposed for the tests that need to look at a view.
func FsOf(c *Compiler) afero.Fs { return c.baseFs }
