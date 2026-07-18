package gen

import (
	"fmt"
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// The mapping rules turn one DSL *engine.Attribute into the exported Go accessor
// (name + type + body) that pkg/schema hand-writes today. See the plan file for
// the derivation of every rule below.

// commonAttrs are the attributes every resource group shares — the DSL's
// TaubyteAttributes block (id/name/description/tags) — derived as the attrs
// present in ALL Resource groups, in DSL order. The generator never restates the
// common schema; it reads it back off the walk, so it cannot drift. "name" is
// the resource identity (Name() reads the struct field), never a config accessor.
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

// noSetter / noGetter report the attribute's accessor-suppression annotations
// (NoSetter/NoGetter/NoAccessors). An attribute with both suppressed has no
// mechanical accessor at all (the old skipBoth case).
func noSetter(a *engine.Attribute) bool { b, _ := a.Meta["noSetter"].(bool); return b }
func noGetter(a *engine.Attribute) bool { b, _ := a.Meta["noGetter"].(bool); return b }

// noStructField reports the NoStructField() annotation — the attribute projects
// to no structureSpec struct field (and no TS wire/session field).
func noStructField(a *engine.Attribute) bool { b, _ := a.Meta["noStructField"].(bool); return b }

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

// compatSegs is the legacy ALIAS location, if the attribute declares one. The
// engine falls back to it when the canonical Path is absent, so generated
// getters do the same (canonical read, compat read-fallback).
func compatSegs(a *engine.Attribute) (segs []string, ok bool) {
	if len(a.Compat) == 0 {
		return nil, false
	}
	return plainSegs(a.Compat)
}

// accessorName is the exported Go name: an Accessor() override, else the last
// plain Path segment title-cased, else the attribute name title-cased.
func accessorName(group string, a *engine.Attribute) string {
	if ov, ok := a.Meta["accessor"].(string); ok && ov != "" {
		return ov
	}
	base := a.Name
	if segs, ok := plainSegs(a.Path); ok && len(segs) > 0 {
		base = segs[len(segs)-1]
	}
	return title(base)
}

func setBody(segs []string) string {
	q := quoteAll(segs)
	if len(segs) == 1 {
		return fmt.Sprintf("return basic.Set(%s, value)", q[0])
	}
	return fmt.Sprintf("return basic.SetChild(%s, %s, value)", q[0], q[1])
}

func getBody(goT string, segs []string) string {
	return fmt.Sprintf("return basic.Get[%s](g, %s)", goT, strings.Join(quoteAll(segs), ", "))
}

// getBodyCompat reads the canonical path, falling back to the compat alias when
// the canonical key is absent (mirrors the tcc engine's Path-then-Compat read in
// engine/node.go). Used for every canonical getter that declares a compat, so
// old on-disk data under the legacy key still reads.
func getBodyCompat(goT string, path, compat []string) string {
	return fmt.Sprintf("var v %s\nif %s.Value(&v) == nil {\nreturn v\n}\n%s",
		goT, chain(path), getBody(goT, compat))
}

// chain builds a g.Config().Get(...).Get(...) query for a config path.
func chain(segs []string) string {
	var b strings.Builder
	b.WriteString("g.Config()")
	for _, seg := range segs {
		fmt.Fprintf(&b, ".Get(%q)", seg)
	}
	return b.String()
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func quoteAll(segs []string) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
