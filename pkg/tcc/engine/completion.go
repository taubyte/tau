package engine

// Field completion: the DSL-derived candidates for a field's value. Static
// candidates (enum members, string-shape literals) come straight from the
// constraint specs; a reference field additionally names a resource group whose
// in-scope instances are candidates (the caller lists those from the live config,
// since the DSL can't know them). Same introspection as validation, read the
// other way: "what may go here" instead of "is this allowed".

// FieldCompletion describes how a field's value can be completed.
type FieldCompletion struct {
	Values    []string // fixed candidates: enum members + shape literals (e.g. ".")
	RefGroup  string   // if non-empty, instances of this resource group are candidates
	RefPrefix string   // prefix to prepend to each referenced name (e.g. "libraries/")
}

// Completion returns the completion sources for one field of a resource group and
// whether the field is known (an attribute at that path, canonical or compat).
// found is false for an unknown path so a caller can distinguish "no candidates"
// (a known free-form field: cid/fqdn/pattern/plain string) from "unknown field".
func Completion(root []*Node, group string, field []string) (fc FieldCompletion, found bool) {
	a := findAttr(root, group, field)
	if a == nil {
		return FieldCompletion{}, false
	}
	found = true
	if enum, ok := a.Meta["enum"].([]string); ok {
		fc.Values = append(fc.Values, enum...)
	}
	if sh, ok := a.Meta["shape"].(ShapeSpec); ok {
		fc.Values = append(fc.Values, sh.Literals...)
	}
	if ref, ok := a.Meta["ref"].(RefSpec); ok {
		fc.RefGroup = ref.Group
		fc.RefPrefix = ref.Prefix
	}
	return fc, true
}

// findAttr locates the attribute of a resource group addressed by field (see
// matchField); nil for an unknown path.
func findAttr(root []*Node, group string, field []string) *Attribute {
	a, _ := matchField(root, group, field)
	return a
}

// matchField locates the attribute addressed by field. It matches an attribute's
// canonical Path or a legacy Compat alias (the paths the accessors accept), and
// also a single element of a list field addressed by a trailing numeric index —
// e.g. ["trigger","domains","0"] resolves to the "trigger/domains" StringSlice, so
// per-element validation/completion of a list works the same as the whole field.
// element reports that latter case (the value is one scalar item, not the list).
// Returns (nil, false) for an unknown path.
func matchField(root []*Node, group string, field []string) (a *Attribute, element bool) {
	if a := matchAttr(root, group, field); a != nil {
		return a, false
	}
	if n := len(field); n > 0 && isIndex(field[n-1]) {
		if a := matchAttr(root, group, field[:n-1]); a != nil && a.Type == TypeStringSlice {
			return a, true
		}
	}
	return nil, false
}

func matchAttr(root []*Node, group string, field []string) *Attribute {
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			if fieldPathEq(fieldPath(a), field) || fieldPathEq(compatPath(a), field) {
				return a
			}
		}
	}
	return nil
}

func isIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
