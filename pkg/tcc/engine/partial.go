package engine

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

// CheckFields returns the authored paths of every field of a resource group that
// carries a partial-checkable constraint — a single-value validator OR a reference
// (dynamic Either/Key paths are skipped). It is what a per-resource partial
// validation iterates: the single-value ones are validated directly, the reference
// ones are checked for existence against the config's in-scope resources.
func CheckFields(root []*Node, group string) [][]string {
	var out [][]string
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			_, hasRef := a.Meta["ref"].(RefSpec)
			if a.Validator == nil && !hasRef {
				continue
			}
			if p := fieldPath(a); p != nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// ValidateField runs the single-value validator for one field (by authored path)
// of a resource group against value. Returns nil when the field has no validator
// or isn't found (unknown/dynamic paths are permissive).
func ValidateField(root []*Node, group string, field []string, value any) error {
	for _, vf := range ValidatedFields(root, group) {
		if fieldPathEq(vf.Path, field) {
			return vf.Validate(value)
		}
	}
	return nil
}

// fieldPath is an attribute's plain authored path (its Path, or its bare name);
// nil if any segment is dynamic (Either/All).
func fieldPath(a *Attribute) []string {
	if len(a.Path) == 0 {
		return []string{a.Name}
	}
	out := make([]string, 0, len(a.Path))
	for _, p := range a.Path {
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
