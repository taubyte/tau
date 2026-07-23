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

// Completion returns the completion sources for one field of a resource group.
// Empty (zero value) when the field has no enumerable candidates (free-form:
// cid/fqdn/pattern/plain strings).
func Completion(root []*Node, group string, field []string) FieldCompletion {
	a := findAttr(root, group, field)
	if a == nil {
		return FieldCompletion{}
	}
	var fc FieldCompletion
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
	return fc
}

// findAttr locates the attribute of a resource group whose authored path — its
// canonical Path or a legacy Compat alias, the same paths the accessors accept —
// matches field; nil if not found.
func findAttr(root []*Node, group string, field []string) *Attribute {
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
