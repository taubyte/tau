package schema

import "sort"

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
