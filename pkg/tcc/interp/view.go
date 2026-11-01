package interp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/engine"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// The lowered view: the project after lowering, in memory. What runs is queues
// and functions, so what a workflow becomes must be in the compiled object.
// Nothing is written to disk: produced trees go to yaseer over an in-memory
// layer dropped when the compile ends, and the compiler over that layer treats
// them as config.

// Layout says where a document lives, as templates over `{kind}` and `{name}`. The
// vocabulary owns the answer (it is the same one decompile writes by), so it is passed
// in rather than assumed here.
type Layout struct {
	Resource string // "{kind}/{name}.yaml"
	// Scope takes `{kind}` too: a scope is a directory named after its container,
	// and a vocabulary may declare more than one kind.
	Scope string
}

// lower compiles the project again over the lowered view, and records beside the result
// what was lowered — so the object says which of its resources nobody wrote, and
// decompile can stop at what somebody did.
func (c *Compiler) lower(ctx context.Context, authored Object, validations []NextValidation) (Object, []NextValidation, error) {
	return c.lowerFor(ctx, c.bound(), authored, validations)
}

// lowerFor reads the lowered view under the same BINDING — the second pass reads the
// same documents back, so it needs the same binding the first pass did.
func (c *Compiler) lowerFor(ctx context.Context, b engine.Bound, authored Object, validations []NextValidation) (Object, []NextValidation, error) {
	if c.layout.Resource == "" {
		return nil, nil, errors.New("interp: this schema declares lowerings but no layout, so nothing can be lowered — see WithLayout")
	}

	docs, adds, scopeKinds, moves := placements(c.compileRoot, authored.Flat(), c.layout)
	if len(docs) == 0 && len(moves) == 0 {
		return authored, validations, nil
	}
	// A produced resource must have a name, its address; a Named reading a field the
	// element lacks is refused here, before anything is written, against what
	// somebody wrote.
	for _, d := range docs {
		if d.name == "" {
			return nil, nil, fmt.Errorf("interp: %s lowers into a %s whose name could not be filled in; every field its name reads has to have a value",
				d.src, d.kind)
		}
	}

	// An entry never invents the resource it lands on; the audit reports the bad
	// name.
	adds = onlyOnWhatExists(c.compileRoot, c.layout, authored.Flat(), docs, adds, scopeKinds)

	// A name a lowering takes must be free: writing over an authored resource would
	// replace somebody's config.
	if err := free(c.compileRoot, authored.Flat(), docs, scopeKinds); err != nil {
		return nil, nil, err
	}

	view, added, err := c.view(moves, docs, adds)
	if err != nil {
		return nil, nil, err
	}
	c.loweredView = view
	// Only this second pass selects Lowerable's container shape.
	loweredRoot := LoweredRoot(c.compileRoot)
	lowered, err := New(engine.SchemaDefinition(loweredRoot), loweredRoot, append(c.viewOptions(), WithVirtual(view, "/"))...)
	if err != nil {
		return nil, nil, err
	}
	obj, moreValidations, err := lowered.AuthoredFor(ctx, b)
	if err != nil {
		return nil, nil, err
	}
	// The record carries what was added, not what was planned: an op the author also
	// wrote by hand must not be deleted on decompile.
	if err := record(obj, recordOf(loweredRoot, obj, docs, added)); err != nil {
		return nil, nil, err
	}
	return obj, moreValidations, nil
}

// free reports the first name a lowering takes that is not its to take: an
// author's, or one another part of the same lowering produced.
func free(root *engine.Node, flat map[string]any, docs []placement, kinds map[string]string) error {
	// Keyed by the source as a person looks for it: two steps producing one name
	// share an id.
	taken := map[string]string{}
	// A scope is taken by name, whatever its kind: everything resolving a name walks
	// scope names.
	scopes := map[string]placement{}
	for _, d := range docs {
		key := d.scope + "\x00" + d.kind + "\x00" + d.name
		if by, clash := taken[key]; clash {
			return fmt.Errorf("interp: %s/%s is produced twice, by %s and by %s", d.kind, d.name, by, d.src)
		}
		taken[key] = d.src
		if kind := kinds[d.scope]; loweredShapeOf(iterOfKind(root, kind)) != nil {
			cut := strings.LastIndex(d.scope, "/")
			parent, name := "", d.scope
			if cut >= 0 {
				parent, name = d.scope[:cut], d.scope[cut+1:]
			}
			key := parent + "\x00" + name
			if by, clash := scopes[key]; clash && by.from != d.from {
				return fmt.Errorf("interp: the scope %s is produced twice, by %s and by %s", d.scope, sourceLabel(by), sourceLabel(d))
			}
			scopes[key] = d
		}
	}

	containers := ContainerKeys(root)
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
				if by, clash := scopes[at+"\x00"+name]; clash && slices.Contains(containers, group) && err == nil {
					err = fmt.Errorf("interp: %s/%s is written here AND is the scope %s becomes; two scopes in one place cannot share a name", group, name, sourceLabel(by))
				}
				child := name
				if at != "" {
					child = at + "/" + name
				}
				walk(res, child)
			}
		}
	}
	if inner, ok := flat[ProjectKey].(map[string]any); ok {
		flat = inner
	}
	walk(flat, "")
	return err
}

func sourceLabel(p placement) string {
	if cut := strings.Index(p.src, " ("); cut >= 0 {
		return p.src[:cut]
	}
	return p.src
}

// view is the project with everything a lowering produces in it, in memory; the
// files on disk are never opened for writing.
func (c *Compiler) view(moves []relocation, docs []placement, adds []granted) (afero.Fs, []granted, error) {
	base := c.baseFs
	if c.basePath != "" {
		base = afero.NewBasePathFs(base, c.basePath)
	}
	view, err := overlay(base)
	if err != nil {
		return nil, nil, err
	}

	// Read the authored flat documents before removing them from the private view. A
	// fresh seer is built afterwards so its path cache sees the new directory shape.
	sourceSeer, err := yaseer.New(yaseer.VirtualFS(view, "/"))
	if err != nil {
		return nil, nil, err
	}
	trees := make([]any, len(moves))
	for i, move := range moves {
		if err := at(sourceSeer, move.from).Value(&trees[i]); err != nil {
			return nil, nil, fmt.Errorf("reading lowerable source at %s: %w", strings.Join(move.from, "/"), err)
		}
		file := "/" + strings.Join(move.from, "/") + ".yaml"
		if err := view.Remove(file); err != nil && !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("relocating lowerable source at %s: %w", strings.Join(move.from, "/"), err)
		}
	}

	seer, err := yaseer.New(yaseer.VirtualFS(view, "/"))
	if err != nil {
		return nil, nil, err
	}

	// Put each authored lowerable source in the leaf document of its compiled container
	// shape. This is a copy in the overlay: the flat authored file stays in the repository
	// and is still what the first pass reads next time.
	for i, move := range moves {
		if err := at(seer, move.to).Set(trees[i]).Commit(); err != nil {
			return nil, nil, fmt.Errorf("placing lowerable source at %s: %w", strings.Join(move.to, "/"), err)
		}
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
		was, err := grant(seer, g, listAt(c.compileRoot, g.on.Kind, g.path))
		if err != nil {
			return nil, nil, fmt.Errorf("writing %s at %s of %s/%s: %w",
				g.value, strings.Join(g.path, "/"), g.on.Kind, g.on.Name, err)
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

// viewOptions is everything about this compiler except where it reads from.
func (c *Compiler) viewOptions() []Option {
	var out []Option
	for k, v := range c.env {
		out = append(out, WithEnv(k, v))
	}
	// The declared values travel too: the second pass reads the same variant
	// spellings.
	for name, values := range c.varValues {
		out = append(out, WithVariable(name, values...))
	}
	return out
}

// overlay is a private in-memory copy of the project: lowering turns a flat
// document into a directory, which a copy-on-write layer cannot express with no
// whiteout.
func overlay(base afero.Fs) (afero.Fs, error) {
	layer := afero.NewMemMapFs()
	err := afero.Walk(base, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return err
		}
		if info.IsDir() {
			return layer.MkdirAll(path, info.Mode())
		}
		body, err := afero.ReadFile(base, path)
		if err != nil {
			return err
		}
		return afero.WriteFile(layer, path, body, info.Mode())
	})
	if err != nil {
		return nil, err
	}
	return layer, nil
}

// listAt reports whether the attribute a path ends at holds a LIST, which is the schema's
// answer and the only thing that decides whether a value is appended or set. The path is
// walked the way it is written: nested group keys, entry names, and last the field — a
// group's entries all share one iterator, so an entry's own name is skipped over.
func listAt(root *engine.Node, kind string, path []string) bool {
	iter := iterOfKind(root, kind)
	for i := 0; iter != nil && i < len(path); i++ {
		if a := attrNamed(iter, path[i]); a != nil {
			return a.Type == engine.TypeStringSlice
		}
		child := childGroup(iter, path[i])
		if child == nil {
			return false
		}
		iter = child
		i++ // the next segment names the entry, and every entry shares this iterator
	}
	return false
}

// childGroup is the iterator of a nested group of this kind, by its key.
func childGroup(iter *engine.Node, key string) *engine.Node {
	for _, child := range iter.Children {
		if k, _ := child.Match.(string); k == key && len(child.Children) > 0 {
			return child.Children[0]
		}
	}
	return nil
}

func attrNamed(iter *engine.Node, name string) *engine.Attribute {
	for _, a := range iter.Attributes {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// grant writes one value into an existing resource, leaving the rest alone. An
// author's own value is never replaced: appended to a list when absent, set on a
// scalar only when empty, which the schema decides. It reports whether it
// changed anything, so the record never claims an author's value.
func grant(seer *yaseer.Seer, g granted, isList bool) (bool, error) {
	q := at(seer, g.at)
	for _, seg := range g.path {
		q = q.Get(seg)
	}

	if !isList {
		var have string
		q.Value(&have)
		if have != "" {
			return false, nil
		}
		return true, q.Set(g.value).Commit()
	}

	var have []string
	// An absent list is not an error: this is the first value at a path nobody has
	// written to.
	q.Value(&have)
	if slices.Contains(have, g.value) {
		return false, nil
	}
	return true, q.Set(append(have, g.value)).Commit()
}

// Root is the schema this compiler compiles against, for a caller that needs to ask the
// vocabulary a question about what it just compiled.
func (c *Compiler) Root() *engine.Node { return c.compileRoot }
