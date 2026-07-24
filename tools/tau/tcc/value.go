package tcc

import "slices"

// Runtime value helpers over a resource document (the plain nested map the
// session reads and writes). Mirrors web-console src/tcc/descriptor.ts.

// Doc is one resource's document.
type Doc map[string]any

func Get(v any, path []string) any {
	cur := v
	for _, seg := range path {
		switch m := cur.(type) {
		case Doc:
			cur = m[seg]
		case map[string]any:
			cur = m[seg]
		default:
			return nil
		}
	}
	return cur
}

// Set writes value at path, creating intermediate maps. A nil or empty-string
// value deletes the key, so blank fields stay out of the YAML.
func Set(d Doc, path []string, value any) {
	if len(path) == 0 {
		return
	}
	cur := d
	for _, seg := range path[:len(path)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[seg] = next
		}
		cur = next
	}
	last := path[len(path)-1]
	if value == nil || value == "" {
		delete(cur, last)
		return
	}
	cur[last] = value
}

// ActiveBranch is whichever alternative key of a dynamic selector exists.
func ActiveBranch(d Doc, f Field) string {
	for _, alt := range f.Alternatives {
		if Get(d, append(append([]string{}, f.BranchPrefix...), alt)) != nil {
			return alt
		}
	}
	return ""
}

// WritePath is a field's concrete path, substituting the active branch for a
// dynamic one (defaulting to the first alternative when none is chosen).
func WritePath(d Doc, f Field) []string {
	if len(f.Alternatives) == 0 {
		return f.Path
	}
	branch := ActiveBranch(d, f)
	if branch == "" {
		branch = f.Alternatives[0]
	}
	return append(append(append([]string{}, f.BranchPrefix...), branch), f.BranchSuffix...)
}

// SwitchBranch selects one alternative of a dynamic selector, dropping the
// others.
func SwitchBranch(d Doc, f Field, choice string) {
	for _, alt := range f.Alternatives {
		if alt != choice {
			Set(d, append(append([]string{}, f.BranchPrefix...), alt), nil)
		}
	}
	p := append(append([]string{}, f.BranchPrefix...), choice)
	if Get(d, p) == nil {
		Set(d, p, map[string]any{})
	}
}

func matches(c *Condition, d Doc) bool {
	got, _ := Get(d, c.Path).(string)
	return slices.Contains(c.In, got)
}

// Visible reports whether a field applies to the current document: its own
// show-when, its section's, and — for a plain field living under a dynamic
// alternative (object/versioning, streaming/ttl, source/github/*) — whether
// that branch is the active one.
func (f *Form) Visible(fd Field, d Doc) bool {
	if fd.ShowWhen != nil && !matches(fd.ShowWhen, d) {
		return false
	}
	for _, s := range f.Sections {
		if s.ID == fd.Section && s.ShowWhen != nil && !matches(s.ShowWhen, d) {
			return false
		}
	}
	if !fd.IsSelector && len(fd.Alternatives) == 0 {
		for _, sel := range f.selectors {
			if len(fd.Path) <= len(sel.BranchPrefix) {
				continue
			}
			at := fd.Path[len(sel.BranchPrefix)]
			for _, alt := range sel.Alternatives {
				if at == alt && ActiveBranch(d, sel) != alt {
					return false
				}
			}
		}
	}
	return true
}

// Selectors are the dynamic branch discriminators, in DSL order.
func (f *Form) Selectors() []Field { return f.selectors }
