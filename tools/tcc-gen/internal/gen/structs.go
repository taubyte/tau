package gen

import (
	"fmt"
	"strings"

	engine "github.com/taubyte/tau/pkg/tcc/engine"
)

// StructModel is one kind projected into one field per logical attribute, for the
// TypeScript generator.
type StructModel struct {
	Spec   string  // the type name, e.g. "Function"
	Fields []Field // one per logical attribute, common first

	// Bare marks the scope container, which carries the common fields and
	// nothing else. It is a DIRECTORY rather than a thing that decodes, so a
	// consumer emitting decode surfaces skips it.
	Bare bool
}

// Field is one struct field. Name/Type drive the Go struct emit; Required
// and Enum are extra DSL facts the TypeScript emit uses (optionality, unions)
// and the Go template ignores.
type Field struct {
	Name       string
	Type       string
	Required   bool
	Enum       []string // permitted values (InSet); empty = none
	EnumString bool     // enum literals are strings (quote them)
}

// Structs projects each DSL resource group into a struct, including every attribute
// regardless of how it is read or written.
func Structs(root []*engine.Node) ([]*StructModel, error) {
	var out []*StructModel
	common := attrSet(commonAttrs(root))
	for _, g := range root {
		name, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		// A group emits a struct if the DSL declares it a Resource, or if it is a nested
		// container group holding resource sub-groups, which compiles to a bare struct of the
		// common fields named by its Singular() (applications -> Application).
		var m *StructModel
		if d, ok := descriptorFor(iter); ok {
			m = &StructModel{Spec: d.Spec, Fields: commonFields(root)}
		} else if iter.Group && len(iter.Children) > 0 {
			so, ok := iter.Meta["singular"].(string)
			if !ok || so == "" {
				return nil, fmt.Errorf("container group %q has no Singular() declaration", name)
			}
			m = &StructModel{Spec: so, Fields: commonFields(root), Bare: true}
		} else {
			continue
		}
		reserved := map[string]bool{}
		for _, f := range commonFields(root) {
			reserved[f.Name] = true
		}
		for _, a := range iter.Attributes {
			if common[a.Name] {
				continue
			}
			gt := goType(a.Type)
			if gt == "" {
				continue
			}
			nm := structFieldName(name, a)
			if reserved[nm] {
				// The name collides with a common field. The attribute keeps its
				// own key in the compiled object; what it loses is a second
				// spelling of a field that is already there.
				continue
			}
			reserved[nm] = true
			if sc, ok := a.Meta["scalar"].(engine.ScalarSpec); ok && sc.GoType != "" {
				gt = sc.GoType
			}
			f := Field{Name: nm, Type: gt, Required: a.Required}
			if enum, ok := a.Meta["enum"].([]string); ok {
				f.Enum = enum
				_, f.EnumString = a.Meta["enumString"].(bool)
			}
			m.Fields = append(m.Fields, f)
		}
		out = append(out, m)
	}
	return out, nil
}

// commonFields are the struct fields every resource carries, derived from the
// DSL's shared attributes (id/name/description/tags) — name, type and the
// Required flag all read off the attribute, so the struct block never restates
// the common schema.
func commonFields(root []*engine.Node) []Field {
	var out []Field
	for _, a := range commonAttrs(root) {
		out = append(out, Field{Name: title(a.Name), Type: goType(a.Type), Required: a.Required})
	}
	return out
}

// structFieldName is the Field("...") override if present, else the accessor name
// with hyphenated fallbacks sanitized into a valid Go identifier ("git-provider"
// -> "GitProvider").
func structFieldName(group string, a *engine.Attribute) string {
	if f, ok := a.Meta["field"].(string); ok && f != "" {
		return f
	}
	nm := accessorName(group, a)
	if !strings.Contains(nm, "-") {
		return nm
	}
	parts := strings.Split(nm, "-")
	for i := range parts {
		parts[i] = title(parts[i])
	}
	return strings.Join(parts, "")
}
