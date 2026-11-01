package interp

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// Where what a lowering produces goes: new resources and values written into
// existing ones, applied to an in-memory layer over the project, addressed as
// documents and serialised by yaseer.

// placement is one resource a lowering produces, and where it belongs.
type placement struct {
	at   []string       // document address: scope dirs, kind, name
	tree map[string]any // the resource as it would have been written
	// what it came from, kept for the provenance the compiled object carries
	id, kind, name, scope string
	from                  string // the authored resource's id
	part                  string // where inside it — "steps/0"
	// src is that resource as a person looks for it, "workflow/checkout (steps/0)":
	// two parts of one lowering share an id.
	src string
}

// relocation moves an authored lowerable resource's document into the leaf of the
// container shape used by the second pass. It is a copy in the in-memory overlay; the
// repository and its authored flat document are untouched.
type relocation struct{ from, to []string }

// granted is one value a lowering puts into a resource somebody else wrote.
type granted struct {
	at    []string // that resource's document address
	on    End      // and which resource it is
	path  []string // where inside it
	value string
	from  string // the authored resource whose lowering wrote it
	scope string // where that resource lives, for finding its compiled id

	// scopes is where the name might resolve, innermost first, collapsed to scope by
	// onlyOnWhatExists. One lowering writes on both sides of the scope it made, so it
	// walks outward as a reference does.
	scopes []string
}

// scopeKinds is what kind of scope each scope path is: a scope is a directory
// named after its container, so `shop/checkout` is
// `namespace/shop/workflow/checkout` on disk. It covers existing scopes and the
// ones this compile will produce.
func scopeKinds(root *engine.Node, obj map[string]any, plan []Derived) map[string]string {
	containers := ContainerKeys(root)
	_, nameField := engine.IdentityOf(root)
	out := map[string]string{}

	var walk func(scope map[string]any, at string)
	walk = func(scope map[string]any, at string) {
		for _, key := range containers {
			instances, ok := scope[key].(map[string]any)
			if !ok {
				continue
			}
			for _, v := range instances {
				res, ok := v.(map[string]any)
				if !ok {
					continue
				}
				name, _ := res[nameField].(string)
				if name == "" {
					continue
				}
				child := within(at, name)
				out[child] = key
				walk(res, child)
			}
		}
	}
	if inner, ok := obj[ProjectKey].(map[string]any); ok {
		obj = inner
	}
	walk(obj, "")

	for _, d := range plan {
		kind, name, ok := strings.Cut(d.Of, "/")
		if !ok || loweredShapeOf(iterOfKind(root, kind)) == nil {
			continue
		}
		// A lowerable source puts every direct product in the scope named after
		// itself. The source is that scope; there is no fourth produced resource.
		if d.Scope == name || strings.HasSuffix(d.Scope, "/"+name) {
			out[d.Scope] = kind
		}
	}
	return out
}

// placements is everything a project's lowerings produce, in a stable order, and
// what kind of scope each scope path is.
func placements(root *engine.Node, obj map[string]any, layout Layout) ([]placement, []granted, map[string]string, []relocation) {
	var docs []placement
	var adds []granted

	plan := LoweringPlan(root, obj)
	kinds := scopeKinds(root, obj, plan)

	for _, d := range plan {
		docs = append(docs, placement{
			at:   placeAt(root, layout, d, kinds),
			tree: authored(root, d),
			id:   d.ID, kind: d.Kind, name: d.Name, scope: d.Scope,
			from: d.Source.ID, part: d.Source.Path, src: d.Source.String(),
		})

		for _, w := range d.Writes {
			if w.On.Kind == "" {
				continue // it is in the resource above, where an author would have written it
			}
			// A scope a lowering names is absolute with a leading slash and otherwise
			// relative to the lowered resource.
			adds = append(adds, granted{
				on:     End{Kind: w.On.Kind, Name: w.On.Name},
				path:   w.Path,
				value:  w.Value,
				from:   d.Source.ID,
				scopes: outward(d.Scope, d.Source.Scope, w.On.Scope),
			})
		}
	}

	sort.SliceStable(docs, func(i, j int) bool {
		return strings.Join(docs[i].at, "/") < strings.Join(docs[j].at, "/")
	})
	return docs, adds, kinds, relocations(root, obj, layout, kinds)
}

// placeAt is where a derived resource's document goes. When the produced kind is itself
// lowerable, it already has the compiled container shape and its fields go in that
// shape's leaf; every other resource remains a flat document.
func placeAt(root *engine.Node, layout Layout, d Derived, kinds map[string]string) []string {
	return loweredAddress(root, layout, d.Scope, d.Kind, d.Name, kinds)
}

func relocations(root *engine.Node, obj map[string]any, layout Layout, kinds map[string]string) []relocation {
	if inner, ok := obj[ProjectKey].(map[string]any); ok {
		obj = inner
	}
	var out []relocation
	walkResources(root, obj, "", func(kind string, iter *engine.Node, _, name string, _ map[string]any, scope string) {
		if loweredShapeOf(iter) == nil {
			return
		}
		out = append(out, relocation{
			from: address(layout, scope, kind, name, kinds),
			to:   loweredAddress(root, layout, scope, kind, name, kinds),
		})
	})
	sort.SliceStable(out, func(i, j int) bool {
		return strings.Join(out[i].to, "/") < strings.Join(out[j].to, "/")
	})
	return out
}

// outward is where a name a lowering wrote might resolve, innermost first, up to
// the source's scope; an absolute name has one candidate.
func outward(produced, source, named string) []string {
	if strings.HasPrefix(named, "/") {
		return []string{within(produced, named)}
	}
	var out []string
	for at := produced; ; {
		out = append(out, within(at, named))
		if at == source || at == "" {
			break
		}
		cut := strings.LastIndex(at, "/")
		if cut < 0 {
			at = ""
			continue
		}
		at = at[:cut]
	}
	return out
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
func address(layout Layout, scope, kind, name string, kinds map[string]string) []string {
	out := scopeDirs(layout, scope, kinds)
	for _, seg := range strings.Split(fill(layout.Resource, kind, name), "/") {
		out = append(out, strings.TrimSuffix(seg, ".yaml"))
	}
	return out
}

func loweredAddress(root *engine.Node, layout Layout, scope, kind, name string, kinds map[string]string) []string {
	out := address(layout, scope, kind, name, kinds)
	if leaf := loweredLeafOf(iterOfKind(root, kind)); leaf != "" {
		out = append(out, leaf)
	}
	return out
}

// scopeDirs is the directories a scope path walks through, each level addressed
// by its kind; an unknown kind is not addressable, with no default to guess.
func scopeDirs(layout Layout, scope string, kinds map[string]string) []string {
	if scope == "" {
		return nil
	}
	var out []string
	at := ""
	for _, level := range strings.Split(scope, "/") {
		at = within(at, level)
		out = append(out, strings.Split(fill(layout.Scope, kinds[at], level), "/")...)
	}
	return out
}

func fill(tmpl, kind, name string) string {
	return strings.NewReplacer("{kind}", kind, "{name}", name).Replace(tmpl)
}

// onlyOnWhatExists resolves each entry's accessor against the project and the
// resources this lowering produces, dropping those found nowhere; the audit
// reports them. It is the first point that knows what exists.
func onlyOnWhatExists(root *engine.Node, layout Layout, flat map[string]any, docs []placement, adds []granted, kinds map[string]string) []granted {
	if inner, ok := flat[ProjectKey].(map[string]any); ok {
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
		for _, scope := range g.scopes {
			if !exists[scope+"\x00"+g.on.Kind+"\x00"+g.on.Name] {
				continue
			}
			g.scope, g.on.Scope = scope, scope
			g.at = loweredAddress(root, layout, scope, g.on.Kind, g.on.Name, kinds)
			out = append(out, g)
			break
		}
	}
	// A TOTAL order, and it has to be taken here: the address an entry is sorted by is
	// only known once its name has resolved, and the order values are written in decides
	// what a list ends up holding.
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := strings.Join(out[i].at, "/"), strings.Join(out[j].at, "/"); a != b {
			return a < b
		}
		a := strings.Join(out[i].path, "/") + "\x00" + out[i].value
		b := strings.Join(out[j].path, "/") + "\x00" + out[j].value
		return a < b
	})
	return out
}
