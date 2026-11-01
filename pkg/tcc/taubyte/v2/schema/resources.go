package schema

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// Instance is one compiled resource and where it lives. Telling a resource from an
// access block, list or namespace is schema knowledge, so consumers use this walk.
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
	if inner, ok := obj[interp.ProjectKey].(map[string]any); ok {
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

// Scopes is every scope in a compiled project (an authored namespace, or a
// lowered resource in its container role), in a stable order; Scope is the one
// holding it. It supplies the `{kind}` of LayoutOf().Scope, since a scope path is
// names only.
func Scopes(obj map[string]any) []Instance {
	root := obj
	if inner, ok := obj[interp.ProjectKey].(map[string]any); ok {
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

				// An entry under a kind inside a resource is either a held resource or an access
				// entry keyed by label; HeldResource decides, as collect() does.
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
