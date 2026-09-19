package schema

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHAT RESOURCES A COMPILED PROJECT HOLDS, and where each one lives.
//
// Walking the compiled object to find them looks trivial and is not: a map of maps is a
// nested group of resources, an access block, a list, or a namespace holding more of the
// same, and telling them apart is schema knowledge. A consumer that walks it itself is
// re-deciding what a resource IS — and the explorer's copy did exactly that, drawing a
// node for every access entry as though `caches: { orders: {...} }` declared a cache
// called orders.
//
// It is the same walk the checks and the relations run, so it is the same function.
//
// Instance rather than Resource because the vocabulary already spends that word on the
// term that DECLARES a kind; this is one of them, compiled.
type Instance struct {
	ID    string
	Kind  string
	Name  string
	Scope string // the namespace path it lives in, "" at the root
	// Fields is the compiled resource itself, so a consumer that has the list does not
	// need to go back to the object to read a value off it.
	Fields map[string]any
}

// Resources is every resource in a compiled project, in a stable order.
func Resources(obj map[string]any) []Instance {
	root := obj
	if inner, ok := obj["object"].(map[string]any); ok {
		root = inner
	}
	all := collect(root, "")
	out := make([]Instance, 0, len(all))
	for _, r := range all {
		out = append(out, Instance{ID: r.id, Kind: r.kind, Name: r.name, Scope: r.scope, Fields: r.fields})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Scopes is every scope in a compiled project — a namespace somebody wrote, or a
// lowerable resource in its compiled container role — as the same Instance a resource
// is reported as, in a stable order. Its Scope is the one HOLDING it, so a scope at the
// root reports "".
//
// It is the one thing Resources() does not list: that lists what is INSIDE a scope, on
// purpose, because a scope is a place rather than a thing an edge points at. Asking for
// the places is a different question, and this is it.
//
// THE KIND IS WHY THIS EXISTS. A scope path is a path of NAMES — `shop/checkout` — which
// is what a reference resolves through and what everything downstream reads, and a name
// carries no kind. A DOCUMENT address does: a scope is a directory named after the
// container it belongs to, so the same path is `namespace/shop/workflow/checkout` on disk.
// LayoutOf().Scope says `{kind}/{name}`, and this is where a consumer gets the `{kind}` —
// rather than keeping its own list of which kinds are scopes, which is the hole §8c is
// about.
func Scopes(obj map[string]any) []Instance {
	root := obj
	if inner, ok := obj["object"].(map[string]any); ok {
		root = inner
	}
	containers := scopeGroups()

	var out []Instance
	var walk func(scope map[string]any, at string)
	walk = func(scope map[string]any, at string) {
		for group, val := range scope {
			if !containers[group] {
				continue
			}
			instances, ok := val.(map[string]any)
			if !ok {
				continue
			}
			for id, v := range instances {
				res, ok := v.(map[string]any)
				if !ok {
					continue
				}

				// ONE KEY, TWO THINGS — and this is the half that was guessing.
				// An entry filed under a kind inside a resource is either a
				// resource the container HOLDS or something that resource
				// declares for ITSELF, keyed by the label of what it reaches.
				// collect() has asked HeldResource since it was written; this
				// walk asked only whether the entry had a `name`, so a
				// container-kind access entry carrying the optional `name`
				// override was reported as a nested scope — with the access
				// LABEL as its id.
				//
				// It mattered beyond tidiness: ScopeKinds() then mapped a path
				// that does not exist to a kind, and pkg/project reads the index
				// and never saw it. Two readers of one compiled object,
				// answering differently about what the project contains.
				if !interp.HeldResource(kindRootNode(), group, id) {
					continue
				}

				name, _ := res["name"].(string)
				if name == "" {
					continue
				}
				out = append(out, Instance{ID: id, Kind: group, Name: name, Scope: at, Fields: res})
				walk(res, strings.TrimPrefix(at+"/"+name, "/"))
			}
		}
	}
	walk(root, "")

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ScopeKinds is what KIND of scope each scope path is, which is what fills in
// LayoutOf().Scope's `{kind}` for every level of a path.
func ScopeKinds(obj map[string]any) map[string]string {
	out := map[string]string{}
	for _, s := range Scopes(obj) {
		out[strings.TrimPrefix(s.Scope+"/"+s.Name, "/")] = s.Kind
	}
	return out
}
