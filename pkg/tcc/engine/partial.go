package engine

import (
	"fmt"
	"slices"
	"strings"
)

// Partial validation: run a resource's declared single-value validators (enum,
// string shape, cid, fqdn, variable-name, minimum, ...) against one field or one
// resource, WITHOUT a compile. This is the cheap path for live editing (UI, IDE,
// tau-cli) — O(fields), no whole-config assembly.
//
// It deliberately covers only single-value constraints. Cross-element constraints
// (Ref existence, cross-app scope) need the assembled name->id index and stay a
// whole-config concern (Compiler.Validate). A caller that wants full coverage runs
// the field/resource check for instant feedback and a whole-config Validate on save.

// ValidatedField pairs a resource field's authored path with the single-value
// validator the DSL declared for it.
type ValidatedField struct {
	Path     []string
	Validate AttributeValidator
}

// ValidatedFields returns every field of resource group `group` in root that
// carries a single-value validator. Fields at a dynamic (Either/Key) path are
// skipped — they have no plain authored path a caller can address.
func ValidatedFields(root []*Node, group string) []ValidatedField {
	var out []ValidatedField
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			if a.Validator == nil {
				continue
			}
			if p := fieldPath(a); p != nil {
				out = append(out, ValidatedField{Path: p, Validate: a.Validator})
			}
		}
	}
	return out
}

// CheckFields returns the authored paths of every partial-checkable field of a
// resource group (dynamic Either/Key paths, which have no plain path, are skipped).
// Every addressable field is checkable: at minimum its declared type is enforced,
// and on top of that any single-value validator and reference existence. It is what
// a per-resource partial validation iterates.
func CheckFields(root []*Node, group string) [][]string {
	var out [][]string
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			if p := fieldPath(a); p != nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// ValidateField runs the single-value validator for one field (by authored path)
// of a resource group against value. It distinguishes three outcomes so a caller
// can tell "valid" from "not recognized":
//   - the field is unknown (no attribute at that plain path) -> an "unknown field"
//     error, so a typo'd path isn't silently reported as OK;
//   - the field is known but carries no single-value validator -> nil (valid,
//     unconstrained);
//   - the field has a validator -> its result.
//
// Before the validator, the value is checked against the field's DECLARED TYPE
// (bool / integer / string / string-array), so a type-only field — one whose sole
// constraint is its type — is still type-safe (a "true6" into a boolean, or a bare
// string into an array, is rejected, not stored as-is).
//
// Fields at a dynamic (Either/Key) path have no plain path and read as unknown.
func ValidateField(root []*Node, group string, field []string, value any) error {
	a, element := matchField(root, group, field)
	if a == nil {
		return fmt.Errorf("unknown field %q on %q", strings.Join(field, "/"), group)
	}
	if err := checkType(a, element, value); err != nil {
		return err
	}
	if a.Validator == nil {
		return nil
	}
	return a.Validator(value)
}

// checkType enforces an attribute's declared type. element is true when addressing
// one item of a list field (e.g. domains/0), whose value must be a scalar string,
// not the list. A nil value (an absent/cleared field) is not type-checked here.
func checkType(a *Attribute, element bool, value any) error {
	if value == nil {
		return nil
	}
	if element { // one element of a StringSlice
		return wantString(a, value)
	}
	switch a.Type {
	case TypeBool:
		if _, ok := value.(bool); !ok {
			return typeErr(a, "boolean", value)
		}
	case TypeInt:
		switch value.(type) {
		case int, int8, int16, int32, int64:
		default:
			return typeErr(a, "integer", value)
		}
	case TypeStringSlice:
		if !isStringList(value) {
			return typeErr(a, "array of strings", value)
		}
	default: // TypeString (incl. Duration/Bytes, authored as strings)
		return wantString(a, value)
	}
	return nil
}

func wantString(a *Attribute, value any) error {
	if _, ok := value.(string); !ok {
		return typeErr(a, "string", value)
	}
	return nil
}

// isStringList reports whether value is a list ([]string or []any) whose every
// element is a string. An empty list is valid; a bare string is not a list.
func isStringList(value any) bool {
	switch t := value.(type) {
	case []string:
		return true
	case []any:
		for _, e := range t {
			if _, ok := e.(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}

func typeErr(a *Attribute, want string, value any) error {
	return fmt.Errorf("field %q expects %s, got %T", a.Name, want, value)
}

// fieldPath is an attribute's plain authored path (its Path, or its bare name);
// nil if any segment is dynamic (Either/All).
func fieldPath(a *Attribute) []string {
	if len(a.Path) == 0 {
		return []string{a.Name}
	}
	return plainStrings(a.Path)
}

// compatPath is an attribute's plain legacy-alias path (Compat), which the
// accessors accept as a read fallback; nil if none or dynamic.
func compatPath(a *Attribute) []string {
	if len(a.Compat) == 0 {
		return nil
	}
	return plainStrings(a.Compat)
}

func plainStrings(path []StringMatch) []string {
	out := make([]string, 0, len(path))
	for _, p := range path {
		s, ok := p.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func fieldPathEq(a, b []string) bool {
	if a == nil || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CompatFields pairs each legacy alias path of a resource group with the
// canonical path it stands for. The DSL already declares both (Path + Compat) —
// this exposes them so a reader can resolve a value authored at the old
// location instead of reading a blank, and a writer can drop the alias when it
// writes the canonical path.
func CompatFields(root []*Node, group string) [][2][]string {
	var out [][2][]string
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			alias, canonical := compatPath(a), fieldPath(a)
			if alias == nil || canonical == nil || slices.Equal(alias, canonical) {
				continue
			}
			out = append(out, [2][]string{alias, canonical})
		}
	}
	return out
}
