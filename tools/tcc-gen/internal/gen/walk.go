package gen

import (
	"strings"

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

type field struct{ name, goType, key string }

// universalFields are the config fields every resource carries, split for uniform
// emission (head before the resource attributes, tail after). Head is the common
// attrs minus name (the template reads name via Name()); the smartops tail is the
// one universal with no DSL attribute — the compiler derives it from tags — so it
// is the only field named here rather than read off the DSL.
func universalFields(root []*engine.Node) (head, tail []field) {
	for _, a := range commonAttrs(root) {
		if a.Name == "name" {
			continue
		}
		head = append(head, field{title(a.Name), goType(a.Type), a.Name})
	}
	return head, []field{{"SmartOps", "[]string", "smartops"}}
}

// containerKey returns the config key of the nested container group — the one
// whose iterator holds resource sub-groups (applications) rather than being a
// resource itself — or "" if the schema has none. Same structural test the
// struct generator uses to emit the bare container struct.
func containerKey(root []*engine.Node) string {
	for _, g := range root {
		if len(g.Children) == 0 {
			continue
		}
		if _, ok := descriptorFor(g.Children[0]); ok {
			continue
		}
		if it := g.Children[0]; it.Group && len(it.Children) > 0 {
			name, _ := g.Match.(string)
			return name
		}
	}
	return ""
}

// containerSpec is the singular Go name for the container group (applications ->
// Application) — the struct type and the parent-container accessor both take it.
func containerSpec(root []*engine.Node) string {
	return title(strings.TrimSuffix(containerKey(root), "s"))
}

// Resources walks the DSL resource groups and projects each into a Resource.
// Only the groups with a Resource descriptor are emitted (the container group
// and leaf maps like clouds are not accessor surfaces).
func Resources(root []*engine.Node) ([]*Resource, error) {
	var out []*Resource
	// The getter template gives every resource a fixed Name()/Get() plus an
	// Application() accessor named for the container group; reserve those so a
	// DSL attribute can never generate an accessor that collides with them.
	container := containerSpec(root)
	common := attrSet(commonAttrs(root))
	head, tail := universalFields(root)
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
		reserved := map[string]bool{"Name": true, container: true, "Get": true}
		for _, f := range append(append([]field{}, head...), tail...) {
			reserved[f.name] = true
		}

		var setters, getters []Accessor
		for _, a := range g.Children[0].Attributes {
			if common[a.Name] {
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

		r.Setters = assemble(head, tail, universalSetters, setters)
		r.Getters = assemble(head, tail, universalGetters, getters)
		// A package-level setter cannot share a name with the resource's exported
		// interface type (e.g. smartops' interface is itself named SmartOps).
		r.Setters = withoutName(r.Setters, d.Iface)

		out = append(out, r)
	}
	return out, nil
}

// assemble prepends the universal head, then the resource accessors, then the
// universal tail, using make to build each accessor from its field spec.
func assemble(head, tail []field, mk func(field) Accessor, mid []Accessor) []Accessor {
	res := make([]Accessor, 0, len(head)+len(mid)+len(tail))
	for _, f := range head {
		res = append(res, mk(f))
	}
	res = append(res, mid...)
	for _, f := range tail {
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
