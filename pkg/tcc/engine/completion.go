package engine

// findAttr locates the attribute of a resource group addressed by field (see
// matchField); nil for an unknown path.
func findAttr(root []*Node, group string, field []string) *Attribute {
	a, _ := matchField(root, group, field)
	return a
}

// matchField locates the attribute addressed by field. It matches an attribute's
// canonical Path, and also a single element of a list field addressed by a trailing numeric index —
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
			if fieldPathEq(fieldPath(a), field) {
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
