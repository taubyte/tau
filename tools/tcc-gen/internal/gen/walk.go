package gen

import (
	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// Accessor is one generated setter or getter.
type Accessor struct {
	Name   string // exported Go name, e.g. "Type"
	GoType string // "string" | "[]string" | "bool" | "int"
	Body   string // full statement(s) incl. the return, e.g. `return basic.Set("id", value)`
	Doc    string // optional doc comment line, e.g. "// Deprecated: use Protocol."
}

// Resource is the template model for one pkg/schema/<pkg> package.
type Resource struct {
	descriptor
	Setters []Accessor
	Getters []Accessor
}

// universalFields are the config fields every resource carries: id/name/description/
// tags come from the DSL's TaubyteAttributes and smartops is parsed from the tags.
// Emitted uniformly (not walked) so ordering/naming stay identical across resources.
// Head is emitted before the resource attributes, tail after.
var (
	universalHead = []field{{"Id", "string", "id"}, {"Description", "string", "description"}, {"Tags", "[]string", "tags"}}
	universalTail = []field{{"SmartOps", "[]string", "smartops"}}
)

type field struct{ name, goType, key string }

// Resources walks the DSL resource groups and projects each into a Resource.
// Only the 9 groups with a descriptor are emitted (applications/clouds are
// special-cased hand-written packages).
func Resources(root []*engine.Node) ([]*Resource, error) {
	var out []*Resource
	for _, g := range root {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		d, ok := descriptorFor(g.Children[0])
		if !ok {
			continue
		}
		r := &Resource{descriptor: d}

		// reserved dedupes accessor names against the universal fields and each
		// other, so a skip-table gap can never emit a duplicate declaration.
		reserved := map[string]bool{"Name": true, "Application": true, "Get": true}
		for _, f := range append(append([]field{}, universalHead...), universalTail...) {
			reserved[f.name] = true
		}

		var setters, getters []Accessor
		for _, a := range g.Children[0].Attributes {
			if commonAttrs[a.Name] {
				continue
			}
			key := name + "." + a.Name
			if a.Key || skipBoth[key] {
				continue // map-key attr, or explicitly non-mechanical
			}
			path, ok := pathSegs(a)
			if !ok {
				continue // matcher in the canonical path — not mechanical
			}
			gt := goType(a.Type)
			if gt == "" {
				continue
			}
			nm := accessorName(name, a)
			if reserved[nm] {
				continue // name already taken (universal field or a prior attr)
			}
			reserved[nm] = true

			// A compat path is a legacy alias. When it has a distinct name we
			// emit a separate deprecated accessor for it; otherwise the canonical
			// getter falls back to it so old on-disk data still reads.
			compat, hasCompat := compatSegs(a)
			aliasName := ""
			if hasCompat {
				aliasName = title(compat[len(compat)-1])
			}
			distinctAlias := hasCompat && aliasName != nm && !reserved[aliasName]

			// Canonical accessors (setters cap at depth 2: basic.Set/SetChild).
			if !skipSet[key] && len(path) <= 2 {
				setters = append(setters, Accessor{Name: nm, GoType: gt, Body: setBody(path)})
			}
			if !skipGet[key] {
				body := getBody(gt, path)
				if hasCompat {
					// Canonical getter always reads path-then-compat so old
					// on-disk data under the legacy key still reads, matching the
					// tcc engine (engine/node.go). A distinct deprecated accessor
					// (below) is still emitted for callers of the old name.
					body = getBodyCompat(gt, path, compat)
				}
				getters = append(getters, Accessor{Name: nm, GoType: gt, Body: body})
			}

			// Deprecated alias accessors pointing at the legacy compat location.
			if distinctAlias {
				reserved[aliasName] = true
				doc := "// Deprecated: use " + nm + "."
				if !skipSet[key] && len(compat) <= 2 {
					setters = append(setters, Accessor{Name: aliasName, GoType: gt, Body: setBody(compat), Doc: doc})
				}
				if !skipGet[key] {
					getters = append(getters, Accessor{Name: aliasName, GoType: gt, Body: getBody(gt, compat), Doc: doc})
				}
			}
		}

		r.Setters = assemble(universalSetters, setters)
		r.Getters = assemble(universalGetters, getters)
		// A package-level setter cannot share a name with the resource's exported
		// interface type (e.g. smartops' interface is itself named SmartOps).
		r.Setters = withoutName(r.Setters, d.Iface)

		out = append(out, r)
	}
	return out, nil
}

// assemble prepends the universal head, then the resource accessors, then the
// universal tail, using make to build each accessor from its field spec.
func assemble(mk func(field) Accessor, mid []Accessor) []Accessor {
	res := make([]Accessor, 0, len(universalHead)+len(mid)+len(universalTail))
	for _, f := range universalHead {
		res = append(res, mk(f))
	}
	res = append(res, mid...)
	for _, f := range universalTail {
		res = append(res, mk(f))
	}
	return res
}

func universalSetters(f field) Accessor {
	return Accessor{Name: f.name, GoType: f.goType, Body: setBody([]string{f.key})}
}

func universalGetters(f field) Accessor {
	return Accessor{Name: f.name, GoType: f.goType, Body: getBody(f.goType, []string{f.key})}
}

// withoutName returns list with any accessor named name removed.
func withoutName(list []Accessor, name string) []Accessor {
	out := list[:0:0]
	for _, a := range list {
		if a.Name != name {
			out = append(out, a)
		}
	}
	return out
}
