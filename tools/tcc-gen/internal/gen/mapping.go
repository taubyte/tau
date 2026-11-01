package gen

import (
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// The mapping rules turn one DSL *engine.Attribute into an exported Go accessor:
// name, type and body.

// commonAttrs are the attributes present in every Resource group (id, name,
// description, tags), in DSL order, read off the walk so they cannot drift. "name" is
// the resource identity, never a config accessor.
func commonAttrs(root []*engine.Node) []*engine.Attribute {
	var iters []*engine.Node
	for _, g := range root {
		if len(g.Children) == 0 {
			continue
		}
		if _, ok := descriptorFor(g.Children[0]); ok {
			iters = append(iters, g.Children[0])
		}
	}
	if len(iters) == 0 {
		return nil
	}
	inAll := map[string]int{}
	for _, it := range iters {
		seen := map[string]bool{}
		for _, a := range it.Attributes {
			if !seen[a.Name] {
				seen[a.Name] = true
				inAll[a.Name]++
			}
		}
	}
	var shared []*engine.Attribute
	seen := map[string]bool{}
	for _, a := range iters[0].Attributes {
		if inAll[a.Name] == len(iters) && !seen[a.Name] {
			seen[a.Name] = true
			shared = append(shared, a)
		}
	}
	return shared
}

// attrSet is the name-set of a list of attributes, for O(1) "is this a common
// attribute?" membership tests during the resource walk.
func attrSet(attrs []*engine.Attribute) map[string]bool {
	m := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		m[a.Name] = true
	}
	return m
}

// goType maps a DSL type to the Go type used by the schema accessors. Float is
// unused by resource schemas; "" signals "skip".
func goType(t engine.Type) string {
	switch t {
	case engine.TypeString:
		return "string"
	case engine.TypeStringSlice:
		return "[]string"
	case engine.TypeBool:
		return "bool"
	case engine.TypeInt:
		return "int"
	default:
		return ""
	}
}

// plainSegs returns the path as plain strings; ok is false if any segment is a
// matcher (Either/All) — those locations are dynamic and not mechanically emittable.
func plainSegs(path []engine.StringMatch) (segs []string, ok bool) {
	for _, p := range path {
		s, isStr := p.(string)
		if !isStr {
			return nil, false
		}
		segs = append(segs, s)
	}
	return segs, true
}

// pathSegs is the CANONICAL config location: Path, or the bare attribute name.
// The tcc engine resolves Path first (engine/node.go setAttributes), so setters
// write here and getters read here.
func pathSegs(a *engine.Attribute) (segs []string, ok bool) {
	if len(a.Path) > 0 {
		return plainSegs(a.Path)
	}
	return []string{a.Name}, true
}

// accessorName is the exported Go name: an Accessor() override, else the last plain
// Path segment title-cased, else the attribute name. A nested Key() is named with "/"
// (`serve/kind`), which no language parses, so its segments are title-cased and
// joined (ServeKind).
func accessorName(group string, a *engine.Attribute) string {
	if ov, ok := a.Meta["accessor"].(string); ok && ov != "" {
		return ov
	}
	if segs, ok := plainSegs(a.Path); ok && len(segs) > 0 {
		return title(segs[len(segs)-1])
	}
	if len(a.Segments) > 1 {
		return joinTitled(a.Segments)
	}
	return title(a.Name)
}

// joinTitled title-cases each segment and joins them: ["serve", "kind"] ->
// "ServeKind".
func joinTitled(segs []string) string {
	parts := make([]string, len(segs))
	for i, s := range segs {
		parts[i] = title(s)
	}
	return strings.Join(parts, "")
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
