package interp

import (
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// A projection is the compiled object as one destination holds it, cut once from
// the object as it runs. The vocabulary says which ids a destination holds; this
// does the structural half: a resource not held is dropped, and an emptied group
// with it; a scope stays while anything in it does; ids are never rewritten; the
// index and records lose entries naming anything dropped. The original is
// untouched.

// Project returns flat as the destination holding exactly the resources `holds`
// answers true for, plus the scopes they sit in. root is the shape the object was
// compiled to — the lowered one, for a vocabulary that lowers.
func Project(root *engine.Node, flat map[string]any, holds func(id string) bool) map[string]any {
	p := newProjector(root)
	p.holds = holds

	out := make(map[string]any, len(flat))
	for key, val := range flat {
		out[key] = val
	}
	if project, ok := flat[ProjectKey].(map[string]any); ok {
		out[ProjectKey], _ = p.scope(project)
	}
	if indexes, ok := flat[IndexesKey].(map[string]any); ok {
		out[IndexesKey] = p.indexes(indexes)
	}
	if rec, ok := flat[LoweredKey].(map[string]any); ok {
		out[LoweredKey] = p.lowered(rec)
	}
	if rec, ok := flat[MountedKey].(map[string]any); ok {
		kept := map[string]any{}
		for id, v := range rec {
			if !p.dropped[id] {
				kept[id] = v
			}
		}
		out[MountedKey] = kept
	}
	return out
}

// Held is every resource and scope id a compiled object holds, sorted, walked the way
// Project walks it — so a projection's Held is exactly what its destination received.
func Held(root *engine.Node, flat map[string]any) []string {
	p := newProjector(root)
	var out []string
	p.holds = func(id string) bool { out = append(out, id); return true }
	if project, ok := flat[ProjectKey].(map[string]any); ok {
		p.scope(project)
	}
	sort.Strings(out)
	return out
}

// newProjector reads which groups hold resources and which are scopes. A group
// declaring no resource is configuration and travels whole with what holds it.
func newProjector(root *engine.Node) projector {
	p := projector{root: root, groups: map[string]bool{}, containers: map[string]bool{}, dropped: map[string]bool{}}
	for _, key := range ContainerKeys(root) {
		p.containers[key] = true
	}
	for _, g := range root.Children {
		key, _ := g.Match.(string)
		if key == "" || len(g.Children) == 0 {
			continue
		}
		if _, ok := g.Children[0].Meta["resource"].([4]string); ok || p.containers[key] {
			p.groups[key] = true
		}
	}
	return p
}

type projector struct {
	root               *engine.Node
	groups, containers map[string]bool
	holds              func(string) bool
	// dropped is every id the walk removed, which is what the index and the records are
	// cleaned of: a segment naming one is an entry pointing at nothing.
	dropped map[string]bool
}

// scope copies one level of the object, keeping what is held, and reports whether
// anything under it was.
func (p projector) scope(level map[string]any) (map[string]any, bool) {
	out := make(map[string]any, len(level))
	found := false
	for key, val := range level {
		instances, ok := val.(map[string]any)
		if !p.groups[key] || !ok {
			out[key] = val
			continue
		}
		kept := make(map[string]any, len(instances))
		for id, v := range instances {
			res, ok := v.(map[string]any)
			// NOT A RESOURCE: an entry this level declares for itself, keyed by the label
			// of what it reaches. It belongs to whatever holds it and goes where that goes.
			if !ok || !HeldResource(p.root, key, id) {
				kept[id] = v
				continue
			}
			if p.containers[key] {
				inner, inside := p.scope(res)
				if p.holds(id) || inside {
					kept[id] = inner
					found = true
					continue
				}
				p.drop(id, res)
				continue
			}
			if p.holds(id) {
				kept[id] = res
				found = true
				continue
			}
			p.dropped[id] = true
		}
		if len(kept) > 0 {
			out[key] = kept
		}
	}
	return out, found
}

// drop records a whole subtree as gone: a scope nothing is held in takes everything in
// it, and every id inside is one the index may name.
func (p projector) drop(id string, res map[string]any) {
	p.dropped[id] = true
	for key, val := range res {
		instances, ok := val.(map[string]any)
		if !p.groups[key] || !ok {
			continue
		}
		for inner, v := range instances {
			m, ok := v.(map[string]any)
			if !ok || !HeldResource(p.root, key, inner) {
				continue
			}
			if p.containers[key] {
				p.drop(inner, m)
				continue
			}
			p.dropped[inner] = true
		}
	}
}

// names reports whether a path written as segments names something dropped.
func (p projector) names(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if p.dropped[seg] {
			return true
		}
	}
	return false
}

// indexes keeps every entry that names nothing dropped. A key whose values were all
// dropped goes too — it held something, and now holds nothing a reader could follow.
// A key that held nothing to begin with is a reservation, and stays.
func (p projector) indexes(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, val := range in {
		if p.names(key) {
			continue
		}
		switch vals := val.(type) {
		case []string:
			kept := make([]string, 0, len(vals))
			for _, v := range vals {
				if !p.names(v) {
					kept = append(kept, v)
				}
			}
			if len(kept) == 0 && len(vals) > 0 {
				continue
			}
			out[key] = kept
		case []any:
			kept := make([]any, 0, len(vals))
			for _, v := range vals {
				if s, ok := v.(string); ok && p.names(s) {
					continue
				}
				kept = append(kept, v)
			}
			if len(kept) == 0 && len(vals) > 0 {
				continue
			}
			out[key] = kept
		case string:
			if !p.names(vals) {
				out[key] = vals
			}
		default:
			out[key] = val
		}
	}
	return out
}

// lowered keeps the provenance of what is held and the entries written onto it.
func (p projector) lowered(rec map[string]any) map[string]any {
	out := make(map[string]any, len(rec))
	for key, val := range rec {
		out[key] = val
	}
	if resources, ok := rec["resources"].(map[string]any); ok {
		kept := map[string]any{}
		for id, v := range resources {
			if !p.dropped[id] {
				kept[id] = v
			}
		}
		out["resources"] = kept
	}
	// THE RECORD IS A LIST OF MAPS, typed one way fresh from a compile and another
	// after a wire format; both are the same entries.
	switch entries := rec["entries"].(type) {
	case []any:
		kept := make([]any, 0, len(entries))
		for _, v := range entries {
			m, _ := v.(map[string]any)
			if on, _ := m["on"].(string); p.dropped[on] {
				continue
			}
			kept = append(kept, v)
		}
		out["entries"] = kept
	case []map[string]any:
		kept := make([]map[string]any, 0, len(entries))
		for _, m := range entries {
			if on, _ := m["on"].(string); p.dropped[on] {
				continue
			}
			kept = append(kept, m)
		}
		out["entries"] = kept
	}
	return out
}
