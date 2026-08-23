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
	// Scope takes `{kind}` too, because a scope is a directory named after the CONTAINER
	// it belongs to and a vocabulary may declare more than one: "{kind}/{name}" addresses
	// an authored namespace and the group a lowering produced with one template. A
	// vocabulary with a single kind of scope may still write it out — "namespace/{name}"
	// means the same thing there — but it stops being true the day it declares a second.
	Scope string // "{kind}/{name}"
}

// lower compiles the project again over the lowered view, and records beside the result
// what was lowered — so the object says which of its resources nobody wrote, and
// decompile can stop at what somebody did.
func (c *Compiler) lower(ctx context.Context, authored Object, validations []NextValidation) (Object, []NextValidation, error) {
	if c.layout.Resource == "" {
		return nil, nil, errors.New("interp: this schema declares lowerings but no layout, so nothing can be lowered — see WithLayout")
	}

	docs, adds, scopeKinds, moves := placements(c.compileRoot, authored.Flat(), c.layout)
	if len(docs) == 0 && len(moves) == 0 {
		return authored, validations, nil
	}
	// AN ENTRY NEVER INVENTS THE RESOURCE IT LANDS ON. A step naming a function that does
	// not exist is a fault the audit reports against the workflow that named it — but the
	// editor creates a document it is asked to write into, so placing the entry anyway
	// produced an empty function with a queue block and no code, and the compile failed
	// with "required attribute 'id'" against a file nobody wrote.
	adds = onlyOnWhatExists(c.compileRoot, c.layout, authored.Flat(), docs, adds, scopeKinds)

	// A NAME A LOWERING TAKES MUST BE FREE. Writing a produced resource over an authored
	// one replaced somebody's config with the compiler's, and the compile carried on: the
	// audit reported the collision afterwards, against an object that had already lost
	// the file it was reporting about.
	if err := free(c.compileRoot, authored.Flat(), docs, scopeKinds); err != nil {
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

	view, added, err := c.view(moves, docs, adds)
	if err != nil {
		return nil, nil, err
	}
	// The vocabulary is the same and the SHAPE is deliberately not: Lowerable carries
	// the authored leaf iterator and its compiled container iterator. Only this second
	// pass selects the latter, so editors and Authored never see compiler-only children.
	loweredRoot := LoweredRoot(c.compileRoot)
	lowered, err := New(engine.SchemaDefinition(loweredRoot), loweredRoot, append(c.viewOptions(), WithVirtual(view, "/"))...)
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
	if err := record(obj, recordOf(loweredRoot, obj, docs, added)); err != nil {
		return nil, nil, err
	}
	return obj, moreValidations, nil
}

// free reports the first name a lowering takes that is not its to take: one an author
// already used, or one another part of the same lowering already produced.
func free(root *engine.Node, flat map[string]any, docs []placement, kinds map[string]string) error {
	// Keyed by the SOURCE as a person would look for it, not by the resource's id: two
	// steps of one workflow that produce the same name have the same id on both sides,
	// so "by X and by X" named the collision and not the two things causing it.
	taken := map[string]string{}
	// A SCOPE IS TAKEN BY NAME, WHATEVER KIND IT IS. Everything that resolves a name walks
	// a chain of scope NAMES — the by-name index is keyed by them, and so is the outward
	// walk a bare reference does — so two scopes of the same name in one place are one
	// scope to every one of them, whatever the compiled object calls each. Keyed without
	// the kind for exactly that reason: the scope `workflow/checkout` becomes, beside an
	// authored `namespace/checkout`, is not two things that never meet — it is one name
	// with two directories behind it.
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

// view is the project with everything a lowering produces in it, in memory. The files on
// disk are never opened for writing — a compiler that edited its input is one nobody could
// trust with a repository.
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
	return out
}

// overlay is a private in-memory copy of the project. Lowering turns a flat source
// document into a DIRECTORY in this view — the same name, a different shape — and a
// copy-on-write filesystem cannot express that: it has no whiteout, so the base file
// stays visible under the directory the layer grew. Copying is what makes the rewrite
// private, and the repository is still never opened for writing.
//
// It was a copy-on-write layer that created only the directories. The cost of copying is
// the project's yaml, which is text a compiler is about to parse anyway; the cost of not
// copying is a compiler that cannot reshape a document, which is the whole of this.
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

// grant writes one value into a resource that already exists, leaving everything else in
// it alone — which is what yaseer is for.
//
// AN AUTHOR'S OWN VALUE IS NEVER REPLACED. Into a list the value is appended when it is
// not already there; into a scalar it goes only when the field is empty. Which of the two
// it is comes from the schema, not from a rule here: a lowering that writes into a list
// and one that writes into a field are the same instruction.
//
// It reports whether it CHANGED anything. A value the author had already written is left
// alone and is not reported — it is theirs, and a record saying otherwise would have
// decompile take it back out of their repository.
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
