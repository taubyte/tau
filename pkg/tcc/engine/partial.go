package engine

import (
	"fmt"
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

// FieldRef is where a field may be authored: its canonical path, plus the legacy
// alias that also satisfies it. Both matter for required-ness — the fixtures
// author a function's domains under the compat key, and a requirement that only
// looked at the canonical path would call them missing.
type FieldRef struct {
	Path   []string
	Compat []string // nil if the field has no legacy alias
}

// RequiredField is a field whose absence the compiler rejects, and the condition
// under which that applies. When is nil for an unconditional Required(); for a
// RequiredWhen it names the sibling that decides, and the values that trigger it.
type RequiredField struct {
	FieldRef
	When   *FieldRef // the discriminator to read, nil when unconditional
	WhenIn []string  // ...required only if its value is one of these
}

// RequiredFields returns a resource group's REQUIRED fields — the ones whose
// absence the compiler rejects at load — each with the condition that governs
// it. Partial validation has to ask for these separately: every other check runs
// against a value, and a missing field has none, so a loop over present values
// can only ever report a resource with nothing in it as valid.
//
// A field at a dynamic (Either/Key) path is skipped, as everywhere else: it has
// no plain authored path to report against. Such a field can still be required
// in effect — a Key attribute with no match already fails at load.
func RequiredFields(root []*Node, group string) []RequiredField {
	var out []RequiredField
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		attrs := g.Children[0].Attributes
		for _, a := range attrs {
			cond, conditional := a.Meta["requiredWhen"].(ConditionSpec)
			if !a.Required && !conditional {
				continue
			}
			p := fieldPath(a)
			if p == nil {
				continue
			}
			rf := RequiredField{FieldRef: FieldRef{Path: p, Compat: compatPath(a)}}
			if conditional {
				// Resolve the discriminator's NAME to its authored path here,
				// where the attribute list is in hand — a consumer holding only
				// paths could not do it without guessing.
				sib := attrByName(attrs, cond.Field)
				if sib == nil {
					continue // names an attribute this group doesn't have
				}
				sp := fieldPath(sib)
				if sp == nil {
					continue // discriminator at a dynamic path — not addressable
				}
				rf.When = &FieldRef{Path: sp, Compat: compatPath(sib)}
				rf.WhenIn = cond.In
			}
			out = append(out, rf)
		}
	}
	return out
}

// RequiredPathOf is an attribute's authored path as a display/lookup key, or ""
// when it sits at a dynamic (Either/Key) location with no plain path.
func RequiredPathOf(a *Attribute) string {
	if p := fieldPath(a); p != nil {
		return strings.Join(p, "/")
	}
	return ""
}

func attrByName(attrs []*Attribute, name string) *Attribute {
	for _, a := range attrs {
		if a.Name == name {
			return a
		}
	}
	return nil
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
		// isInteger covers every whole-number kind, so this cannot drift out of
		// step with what coerceScalar will render for a string field.
		if !isInteger(value) && !convertible(value, TypeInt) { // a quoted number is still a number
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
	if _, ok := value.(string); ok {
		return nil
	}
	// An unquoted number is still a string field's value — YAML inferred a type
	// from notation, the DSL declares meaning. A bool is not: that is a mistake
	// about the field, not a way of writing it.
	if convertible(value, TypeString) {
		return nil
	}
	return typeErr(a, "string", value)
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
