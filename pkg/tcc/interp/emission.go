package interp

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// WHAT ONE RESOURCE ADDS TO A PROJECT.
//
// A lowering produces two kinds of change, and both are needed to answer "what does this
// become": documents that did not exist — the queues a workflow runs on — and entries
// placed on documents that did, because the function that runs a step is what declares
// it receives from that step's queue.
//
// It is computed per AUTHORED resource, which is the only unit anyone asks about: a
// console shows what one workflow becomes, `tau` prints it before applying it, and the
// whole-project view is every emission applied at once. One computation, three readers —
// the alternative is a view that builds the changes and a console that recomputes
// something that looks like them.

// Document is a file a lowering produces.
type Document struct {
	Path string // where it lives, by the vocabulary's layout
	Body string
	Kind string
	Name string
}

// Entry is an access placed on a document that already exists.
type Entry struct {
	Path string // the document it is placed on
	// Who declares it, and what it reaches.
	On   End
	Kind string // the target's kind
	Name string // the target's name
	Op   string
	// Namespace is written into the entry when the target is somewhere else, absolutely.
	// Empty means the two are in the same place and the entry says nothing about it.
	Namespace string
}

// Emission is everything one authored resource lowers to.
type Emission struct {
	Of        Source
	Documents []Document
	Entries   []Entry
}

// Emissions is what every lowerable resource in a project becomes, one per authored
// resource, in a stable order.
func Emissions(root *engine.Node, obj map[string]any, layout Layout) []Emission {
	bySource := map[string]*Emission{}
	var order []string

	for _, d := range LoweringPlan(root, obj) {
		key := d.Source.Scope + "\x00" + d.Source.Kind + "\x00" + d.Source.Name
		e, seen := bySource[key]
		if !seen {
			// The source's own PATH is not carried here: an emission is about the
			// resource, and which element of it produced what is on each document.
			at := d.Source
			at.Path, at.Part = "", nil
			e = &Emission{Of: at}
			bySource[key] = e
			order = append(order, key)
		}

		e.Documents = append(e.Documents, Document{
			Path: documentPath(layout, d.Scope, d.Kind, d.Name),
			Body: d.Doc, Kind: d.Kind, Name: d.Name,
		})

		for _, g := range d.Grants {
			if g.From.Self() {
				continue // it is in the document above, where an author would have written it
			}
			e.Entries = append(e.Entries, entryFor(layout, d, g))
		}
	}

	out := make([]Emission, 0, len(order))
	for _, key := range order {
		e := bySource[key]
		sort.SliceStable(e.Documents, func(i, j int) bool { return e.Documents[i].Path < e.Documents[j].Path })
		sort.SliceStable(e.Entries, func(i, j int) bool {
			if e.Entries[i].Path != e.Entries[j].Path {
				return e.Entries[i].Path < e.Entries[j].Path
			}
			return e.Entries[i].Name < e.Entries[j].Name
		})
		out = append(out, *e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Of.Scope != out[j].Of.Scope {
			return out[i].Of.Scope < out[j].Of.Scope
		}
		if out[i].Of.Kind != out[j].Of.Kind {
			return out[i].Of.Kind < out[j].Of.Kind
		}
		return out[i].Of.Name < out[j].Of.Name
	})
	return out
}

// EmissionOf is what ONE resource becomes — the question a console asks when someone
// opens a workflow, and the one `tau` answers before it applies anything.
//
// It is keyed on the AUTHORED resource, so a lowering that produced something which
// lowers in turn is included: what a reader wants is everything that exists because they
// wrote this, however many steps down it happened.
func EmissionOf(root *engine.Node, obj map[string]any, layout Layout, kind, name, scope string) (Emission, bool) {
	for _, e := range Emissions(root, obj, layout) {
		if e.Of.Kind == kind && e.Of.Name == name && e.Of.Scope == scope {
			return e, true
		}
	}
	return Emission{}, false
}

func entryFor(layout Layout, d Derived, g Access) Entry {
	scope := g.From.Scope
	if scope == "" {
		scope = d.Scope
	}

	// What it reaches: the derived resource itself, or a name the lowering gave.
	target, kind, at := d.Name, d.Kind, d.Scope
	if !g.To.Self() {
		target, kind = g.To.Name, g.To.Kind
		if g.To.Scope != "" {
			at = g.To.Scope
		}
	}

	e := Entry{
		Path: documentPath(layout, scope, g.From.Kind, g.From.Name),
		On:   End{Kind: g.From.Kind, Name: g.From.Name, Scope: scope},
		Kind: kind, Name: target, Op: g.Op,
	}
	// Only written when the two are not in the same place, and then absolutely: a
	// relative path would be read from the accessor's scope, which is not where this
	// was decided.
	if at != scope {
		e.Namespace = "/" + at
	}
	return e
}

// documentPath is where a resource's document lives, by the vocabulary's own layout.
// Absolute, always: an in-memory filesystem keys on the exact path, so "queue/x.yaml"
// is a different file from the "/queue/x.yaml" everything else asks for — and the second
// one silently does not exist.
func documentPath(layout Layout, scope, kind, name string) string {
	return "/" + scopeDir(layout, scope) + fill(layout.Resource, kind, name)
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
