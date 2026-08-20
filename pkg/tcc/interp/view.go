package interp

import (
	"context"
	"os"
	"strings"

	"github.com/spf13/afero"
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

	// The documents FIRST, all of them, and only then the entries: an access can name a
	// resource another lowering produced (an agent's memory is searched by the loop it
	// also produced), and editing a document that is about to be written would edit a
	// file and then have it replaced.
	for _, d := range plan {
		if err := writeDocument(view, layout, d); err != nil {
			return nil, err
		}
	}

	seer, err := yaseer.New(yaseer.VirtualFS(view, "/"))
	if err != nil {
		return nil, err
	}
	for _, d := range plan {
		// An access entry belongs on the resource that DECLARES it, which is usually not
		// the one the lowering produced: the queue is received from by the step's
		// function, and the entry is the function's. The derived document carries the
		// ones it declares itself (see document()), so only the others are placed here.
		for _, g := range d.Grants {
			if g.From.Self() {
				continue
			}
			if err := placeAccess(seer, layout, d, g); err != nil {
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

func writeDocument(fs afero.Fs, layout Layout, d Derived) error {
	// Absolute, always: an in-memory filesystem keys on the exact path, so a document
	// written as "queue/x.yaml" is a different file from the "/queue/x.yaml" everything
	// else asks for — and the second one silently does not exist.
	path := "/" + scopeDir(layout, d.Scope) + fill(layout.Resource, d.Kind, d.Name)
	if dir := path[:strings.LastIndex(path, "/")+1]; dir != "" {
		if err := fs.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return afero.WriteFile(fs, path, []byte(d.Doc), 0o644)
}

// placeAccess writes one access entry onto the document of the resource that declares
// it, leaving the rest of that document alone — which is what yaseer is for.
func placeAccess(seer *yaseer.Seer, layout Layout, d Derived, g Access) error {
	scope := g.From.Scope
	if scope == "" {
		scope = d.Scope
	}
	q := seer.Query()
	for _, dir := range scopeParts(layout, scope) {
		q = q.Get(dir)
	}
	q = q.Get(g.From.Kind).Get(g.From.Name).Document()

	// What it reaches: the derived resource itself, or a name the lowering gave.
	target, kind, at := d.Name, d.Kind, d.Scope
	if !g.To.Self() {
		target, kind = g.To.Name, g.To.Kind
		if g.To.Scope != "" {
			at = g.To.Scope
		}
	}
	entry := q.Get(kind).Get(target)

	// The namespace is only written when the two are not in the same place, and then
	// absolutely: a relative path would be read from the accessor's scope, which is not
	// where this was decided.
	if at != scope {
		if err := entry.Get("namespace").Set("/" + at).Commit(); err != nil {
			return err
		}
	}
	return entry.Get("ops").Set([]string{g.Op}).Commit()
}

// scopeDir is the directory a scope's documents live in, e.g. "shop/eu" ->
// "namespace/shop/namespace/eu/".
func scopeDir(layout Layout, scope string) string {
	parts := scopeParts(layout, scope)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/") + "/"
}

func scopeParts(layout Layout, scope string) []string {
	if scope == "" {
		return nil
	}
	var out []string
	for _, name := range strings.Split(scope, "/") {
		out = append(out, strings.Split(fill(layout.Scope, "", name), "/")...)
	}
	return out
}

func fill(tmpl, kind, name string) string {
	return strings.NewReplacer("{kind}", kind, "{name}", name).Replace(tmpl)
}

// FsOf is where a compiler reads from. Exposed for the tests that need to look at a view.
func FsOf(c *Compiler) afero.Fs { return c.baseFs }
