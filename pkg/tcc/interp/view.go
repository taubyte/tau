package interp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

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
// NOTHING IS WRITTEN TO DISK and nothing is rendered by hand. The view is a copy-on-write
// layer over the project, held in memory: what a lowering produces is a tree, handed to
// yaseer exactly as an editor hands it one, and an authored document that gains an entry
// is edited in the layer with the rest of it left alone. yaseer serialises into that
// layer, which is the only place any yaml exists, and the layer is dropped when the
// compile ends.
//
// What comes back is a compiler over the layer, so everything downstream — reference
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

// lower compiles the project again over the lowered view, and records beside the result
// what was lowered — so the object says which of its resources nobody wrote, and
// decompile can stop at what somebody did.
func (c *Compiler) lower(ctx context.Context, authored Object, validations []NextValidation) (Object, []NextValidation, error) {
	if c.layout.Resource == "" {
		return nil, nil, errors.New("interp: this schema declares lowerings but no layout, so nothing can be lowered — see WithLayout")
	}

	docs, adds := placements(c.compileRoot, authored.Flat(), c.layout)
	if len(docs) == 0 {
		return authored, validations, nil
	}
	// AN ENTRY NEVER INVENTS THE RESOURCE IT LANDS ON. A step naming a function that does
	// not exist is a fault the audit reports against the workflow that named it — but the
	// editor creates a document it is asked to write into, so placing the entry anyway
	// produced an empty function with a queue block and no code, and the compile failed
	// with "required attribute 'id'" against a file nobody wrote.
	adds = onlyOnWhatExists(c.compileRoot, authored.Flat(), docs, adds)

	// A NAME A LOWERING TAKES MUST BE FREE. Writing a produced resource over an authored
	// one replaced somebody's config with the compiler's, and the compile carried on: the
	// audit reported the collision afterwards, against an object that had already lost
	// the file it was reporting about.
	if err := free(c.compileRoot, authored.Flat(), docs); err != nil {
		return nil, nil, err
	}

	// A plan that hit the nesting cap is a plan with the bottom missing, and compiling it
	// would produce a configuration that is wrong in a way nothing else will mention.
	for _, d := range docs {
		if d.depth >= MaxLoweringDepth {
			return nil, nil, fmt.Errorf("interp: %s/%s lowers more than %d levels deep; the rest was not produced",
				d.kind, d.name, MaxLoweringDepth)
		}
	}

	view, added, err := c.view(docs, adds)
	if err != nil {
		return nil, nil, err
	}
	lowered, err := New(c.project, c.compileRoot, append(c.viewOptions(), WithVirtual(view, "/"))...)
	if err != nil {
		return nil, nil, err
	}
	obj, moreValidations, err := lowered.Authored(ctx)
	if err != nil {
		return nil, nil, err
	}
	// The record carries what was ADDED, not what was planned. An author who wrote
	// `ops: [receive]` by hand and a lowering that grants the same op are one entry, and
	// a record claiming the compiler put it there would have decompile delete somebody's
	// config on the way back out.
	if err := record(obj, recordOf(c.compileRoot, obj, docs, added)); err != nil {
		return nil, nil, err
	}
	return obj, moreValidations, nil
}

// free reports the first name a lowering takes that is not its to take: one an author
// already used, or one another part of the same lowering already produced.
func free(root *engine.Node, flat map[string]any, docs []placement) error {
	taken := map[string]string{}
	for _, d := range docs {
		key := d.scope + "\x00" + d.kind + "\x00" + d.name
		if by, clash := taken[key]; clash {
			return fmt.Errorf("interp: %s/%s is produced twice, by %s and by %s", d.kind, d.name, by, d.from)
		}
		taken[key] = d.from
	}

	_, nameField := engine.IdentityOf(root)
	var walk func(scope map[string]any, at string)
	var err error
	walk = func(scope map[string]any, at string) {
		for group, v := range scope {
			instances, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for _, r := range instances {
				res, ok := r.(map[string]any)
				if !ok {
					continue
				}
				name, _ := res[nameField].(string)
				if name == "" {
					continue
				}
				if by, clash := taken[at+"\x00"+group+"\x00"+name]; clash && err == nil {
					err = fmt.Errorf("interp: %s/%s is written here AND is what %s becomes; one of them has to be renamed", group, name, by)
				}
				child := name
				if at != "" {
					child = at + "/" + name
				}
				walk(res, child)
			}
		}
	}
	if inner, ok := flat["object"].(map[string]any); ok {
		flat = inner
	}
	walk(flat, "")
	return err
}

// view is the project with everything a lowering produces in it, in memory. The files on
// disk are never opened for writing — a compiler that edited its input is one nobody could
// trust with a repository.
func (c *Compiler) view(docs []placement, adds []granted) (afero.Fs, []granted, error) {
	base := c.baseFs
	if c.basePath != "" {
		base = afero.NewBasePathFs(base, c.basePath)
	}
	view, err := overlay(base)
	if err != nil {
		return nil, nil, err
	}

	seer, err := yaseer.New(yaseer.VirtualFS(view, "/"))
	if err != nil {
		return nil, nil, err
	}

	// The resources FIRST, all of them, and only then the entries: an entry can land on
	// a resource another lowering produced (an agent's memory is searched by the loop it
	// also produced), and writing the resource afterwards would replace the entry.
	for _, d := range docs {
		if err := at(seer, d.at).Set(d.tree).Commit(); err != nil {
			return nil, nil, fmt.Errorf("placing %s/%s: %w", d.kind, d.name, err)
		}
	}
	added := make([]granted, 0, len(adds))
	for _, g := range adds {
		was, err := grant(seer, g)
		if err != nil {
			return nil, nil, fmt.Errorf("granting %s on %s/%s: %w", g.op, g.on.Kind, g.on.Name, err)
		}
		if was {
			added = append(added, g)
		}
	}
	return view, added, seer.Sync()
}

// at addresses a document the way an editor does: the segments, then the document.
func at(seer *yaseer.Seer, address []string) *yaseer.Query {
	q := seer.Query()
	for _, seg := range address {
		q = q.Get(seg)
	}
	return q.Document()
}

// grant adds one access entry to a resource that already exists, leaving everything else
// in it alone — which is what yaseer is for.
//
// The ops MERGE. Two can name one target — a step whose failure goes back to its own
// queue makes its function both receive from that queue and send to it — and the author
// may have written one by hand. Setting the list replaced whatever was there, and what it
// dropped was invisible: a queue nothing consumes still compiles.
// It reports whether it ADDED anything. An op the author already wrote is left alone and
// is not reported — it is theirs, and a record saying otherwise would have decompile take
// it back out of their repository.
func grant(seer *yaseer.Seer, g granted) (bool, error) {
	entry := at(seer, g.at).Get(g.kind).Get(g.name)
	if g.where != "" && g.whereField != "" {
		// Only when nobody has said where to look. An author who wrote a namespace on
		// this entry meant it, and replacing it would point their access somewhere else
		// without a word.
		var where string
		entry.Get(g.whereField).Value(&where)
		if where == "" {
			if err := entry.Get(g.whereField).Set(g.where).Commit(); err != nil {
				return false, err
			}
		}
	}

	var have []string
	// An absent list is not an error: this is the first op on a target nobody has
	// written about.
	entry.Get(g.verbsField).Value(&have)
	if slices.Contains(have, g.op) {
		return false, nil
	}
	return true, entry.Get(g.verbsField).Set(append(have, g.op)).Commit()
}

// Layout is where this compiler's vocabulary keeps its documents.
func (c *Compiler) Layout() Layout { return c.layout }

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

// Root is the schema this compiler compiles against, for a caller that needs to ask the
// vocabulary a question about what it just compiled.
func (c *Compiler) Root() *engine.Node { return c.compileRoot }
