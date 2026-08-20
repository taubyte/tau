package interp

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// WHERE WHAT A LOWERING PRODUCES GOES, and what it adds to what already exists.
//
// A lowering produces two kinds of change, and the compiler applies both to an in-memory
// layer over the project: resources that did not exist — the queues a workflow runs on —
// and values written into resources that did, because the function that runs a step is
// what has to say it receives from that step's queue.
//
// Both are addressed the way a document is addressed, because that is what they are.
// Nothing here renders yaml: the tree goes to yaseer, which is the same thing an editor
// hands it, and the layer is thrown away when the compile ends.

// placement is one resource a lowering produces, and where it belongs.
type placement struct {
	at   []string       // document address: scope dirs, kind, name
	tree map[string]any // the resource as it would have been written
	// what it came from, kept for the provenance the compiled object carries
	id, kind, name, scope string
	from                  string // the authored resource's id
	part                  string // where inside it — "steps/0"
	depth                 int    // how many lowerings deep this one is
}

// granted is one value a lowering puts into a resource somebody else wrote.
type granted struct {
	at    []string // that resource's document address
	on    End      // and which resource it is
	path  []string // where inside it
	value string
	from  string // the authored resource whose lowering wrote it
	scope string // where that resource lives, for finding its compiled id
}

// placements is everything a project's lowerings produce, in a stable order.
func placements(root *engine.Node, obj map[string]any, layout Layout) ([]placement, []granted) {
	var docs []placement
	var adds []granted

	for _, d := range LoweringPlan(root, obj) {
		docs = append(docs, placement{
			at:   address(layout, d.Scope, d.Kind, d.Name),
			tree: authored(root, d),
			id:   d.ID, kind: d.Kind, name: d.Name, scope: d.Scope,
			from: d.Source.ID, part: d.Source.Path, depth: len(d.Via),
		})

		for _, w := range d.Writes {
			if w.On.Kind == "" {
				continue // it is in the resource above, where an author would have written it
			}
			// A scope a lowering names is read the way a scope is read anywhere: absolute
			// from the root with a leading slash, and otherwise relative to where the
			// resource being lowered lives. Taking it as absolute put `eu` at the root,
			// which is a resource in the wrong namespace and compiles.
			scope := within(d.Scope, w.On.Scope)
			adds = append(adds, granted{
				at:    address(layout, scope, w.On.Kind, w.On.Name),
				on:    End{Kind: w.On.Kind, Name: w.On.Name, Scope: scope},
				path:  w.Path,
				value: w.Value,
				from:  d.Source.ID, scope: scope,
			})
		}
	}

	sort.SliceStable(docs, func(i, j int) bool { return strings.Join(docs[i].at, "/") < strings.Join(docs[j].at, "/") })
	sort.SliceStable(adds, func(i, j int) bool {
		if a, b := strings.Join(adds[i].at, "/"), strings.Join(adds[j].at, "/"); a != b {
			return a < b
		}
		a := strings.Join(adds[i].path, "/") + "\x00" + adds[i].value
		b := strings.Join(adds[j].path, "/") + "\x00" + adds[j].value
		return a < b
	})
	return docs, adds
}

// within reads a scope a lowering named: absolute from the root with a leading slash,
// relative to where the lowered resource lives otherwise, and the same place when it
// names none.
func within(base, scope string) string {
	switch {
	case scope == "":
		return base
	case strings.HasPrefix(scope, "/"):
		return strings.Trim(strings.TrimPrefix(scope, "/"), "/")
	case base == "":
		return scope
	}
	return base + "/" + scope
}

// address is the document address of a resource, by the vocabulary's own layout: the
// scope directories, then the kind, then the name. The same segments an editor walks.
func address(layout Layout, scope, kind, name string) []string {
	var out []string
	if scope != "" {
		for _, level := range strings.Split(scope, "/") {
			out = append(out, strings.Split(fill(layout.Scope, "", level), "/")...)
		}
	}
	for _, seg := range strings.Split(fill(layout.Resource, kind, name), "/") {
		out = append(out, strings.TrimSuffix(seg, ".yaml"))
	}
	return out
}

func fill(tmpl, kind, name string) string {
	return strings.NewReplacer("{kind}", kind, "{name}", name).Replace(tmpl)
}

// onlyOnWhatExists drops an entry whose accessor is nowhere — not in the project, and not
// among the resources this lowering produces. What it names is wrong, and saying so is
// the audit's job; writing to it here would answer a bad name by creating the resource it
// named.
//
// The produced half is not an afterthought: an agent's loop is generated, and the entry
// saying it searches the memory the same lowering generated lands ON it. Checking only
// what was written silently deleted that, and the agent's memory became a resource
// nothing reaches.
func onlyOnWhatExists(root *engine.Node, flat map[string]any, docs []placement, adds []granted) []granted {
	if inner, ok := flat["object"].(map[string]any); ok {
		flat = inner
	}
	_, nameField := engine.IdentityOf(root)
	exists := map[string]bool{}
	var walk func(scope map[string]any, at string)
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
				exists[at+"\x00"+group+"\x00"+name] = true
				// a scope holds resources of its own, at a path of its own
				child := name
				if at != "" {
					child = at + "/" + name
				}
				walk(res, child)
			}
		}
	}
	walk(flat, "")
	for _, d := range docs {
		exists[d.scope+"\x00"+d.kind+"\x00"+d.name] = true
	}

	out := adds[:0]
	for _, g := range adds {
		if exists[g.scope+"\x00"+g.on.Kind+"\x00"+g.on.Name] {
			out = append(out, g)
		}
	}
	return out
}
