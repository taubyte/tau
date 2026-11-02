package engine

import (
	"fmt"
	"slices"
	"strings"
)

// Partial validation runs a resource's single-value validators against one
// field or resource without a compile: the cheap path for live editing.
// Cross-element constraints (Ref existence, cross-app scope) need the whole
// config (Compiler.Validate).

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

type FieldRef struct {
	Path []string
	// AnyOf is every plain location the field may be authored at when its path
	// holds a finite matcher (object/size or streaming/size); AnyOf[0] == Path, nil
	// without a matcher.
	AnyOf [][]string
}

// RequiredField is a field whose absence the compiler rejects, and the condition
// under which that applies. When is nil for an unconditional Required(); for a
// RequiredWhen it names the sibling that decides, and the values that trigger it.
type RequiredField struct {
	FieldRef
	When   *FieldRef // the discriminator to read, nil when unconditional
	WhenIn []string  // ...required only if its value is one of these

	// WhenBranch: the discriminator has no value of its own, its path ending in a
	// finite matcher it Captures(), so its value is the key written under
	// WhenBranchAt:
	//
	//	build:                 # WhenBranchAt = ["build"]
	//	    commands:          # ...and the discriminator's value is "commands"
	//	        image: node:22
	//
	// When is nil then. WhenBranchAt may be empty, the branch sitting at the
	// resource's root, so test this bool, not its length.
	WhenBranch   bool
	WhenBranchAt []string

	// In is where the field's ENTRIES are, when it is a field of each entry of a group
	// or each element of a list (`build/steps`); its paths are then relative to one
	// entry. Empty for a field of the resource itself. List says the entries are a
	// list's elements, addressed by position.
	In   []string
	List bool

	// Claimed is every key under In that the holder's own fields declare, which is not
	// an entry: an agent's `model: {name: …}` is its own field beside the access group
	// of the same name.
	Claimed []string
}

// fieldPaths is every plain authored location of an attribute: each finite
// matcher segment multiplies into its values; an unbounded one (All) yields nil.
func fieldPaths(a *Attribute) [][]string {
	path := a.Path
	if len(path) == 0 {
		return [][]string{{a.Name}}
	}
	out := [][]string{{}}
	for _, seg := range path {
		var vals []string
		switch m := seg.(type) {
		case string:
			vals = []string{m}
		case FiniteMatcher:
			if vals = m.Values(); len(vals) == 0 {
				return nil // matches nothing: no enumerable location
			}
		default:
			return nil // unbounded matcher: not enumerable
		}
		next := make([][]string, 0, len(out)*len(vals))
		for _, prefix := range out {
			for _, v := range vals {
				next = append(next, append(append([]string{}, prefix...), v))
			}
		}
		out = next
	}
	return out
}

// RequiredFields returns a resource group's required fields with the condition
// governing each: a missing field has no value, so a loop over present values
// can never report it. A block's fields are the resource's, one key down; a group's
// or a list's are each entry's (In). A field at a dynamic path is skipped, and so is
// anything inside an entry's own groups.
func RequiredFields(root []*Node, group string) []RequiredField {
	var out []RequiredField
	for _, g := range root {
		name, _ := g.Match.(string)
		if name != group || len(g.Children) == 0 {
			continue
		}
		iter := g.Children[0]
		out = append(out, requiredOf(iter.Attributes, nil, nil, false)...)
		var walk func(holder []*Attribute, children []*Node, at []string)
		walk = func(holder []*Attribute, children []*Node, at []string) {
			for _, c := range children {
				key, _ := c.Match.(string)
				if key == "" || !c.Nested {
					continue
				}
				here := append(append([]string{}, at...), key)
				if IsBlock(c) {
					out = append(out, requiredOf(c.Attributes, here, nil, false)...)
					walk(c.Attributes, c.Children, here)
					continue
				}
				if !c.Group || len(c.Children) == 0 {
					continue
				}
				claimed := claimedAt(holder, key)
				for _, rf := range requiredOf(c.Children[0].Attributes, nil, here, c.Sequence) {
					rf.Claimed = claimed
					out = append(out, rf)
				}
			}
		}
		if iter.Nested {
			walk(iter.Attributes, iter.Children, nil)
		}
	}
	return out
}

// claimedAt is every key under key that one of the holder's own fields declares.
func claimedAt(holder []*Attribute, key string) []string {
	var out []string
	for _, a := range holder {
		for _, p := range fieldPaths(a) {
			if len(p) > 1 && p[0] == key && !slices.Contains(out, p[1]) {
				out = append(out, p[1])
			}
		}
	}
	return out
}

// requiredOf is the required fields of one level: attrs, addressed through prefix
// (a block's key), and filed under in when they are a group's or a list's entries.
func requiredOf(attrs []*Attribute, prefix, in []string, list bool) []RequiredField {
	under := func(p []string) []string { return append(append([]string{}, prefix...), p...) }
	var out []RequiredField
	for _, a := range attrs {
		cond, conditional := a.Meta["requiredWhen"].(ConditionSpec)
		if !a.Required && !conditional {
			continue
		}
		paths := fieldPaths(a)
		if paths == nil {
			continue
		}
		rf := RequiredField{FieldRef: FieldRef{Path: under(paths[0])}, In: in, List: list}
		if len(paths) > 1 {
			for _, p := range paths {
				rf.AnyOf = append(rf.AnyOf, under(p))
			}
		}
		if conditional {
			// Resolve the discriminator's NAME to its authored path here,
			// where the attribute list is in hand — a consumer holding only
			// paths could not do it without guessing.
			sib := attrByName(attrs, cond.Field)
			if sib == nil {
				continue // names an attribute this group doesn't have
			}
			rf.WhenIn = cond.In

			if sp := fieldPath(sib); sp != nil {
				rf.When = &FieldRef{Path: under(sp)}
			} else if at, ok := branchPrefix(sib); ok {
				// A CAPTURED BRANCH: the discriminator has no value of its
				// own, its value is the key under this prefix.
				rf.WhenBranch, rf.WhenBranchAt = true, under(at)
			} else {
				continue // a dynamic path that is not a captured branch
			}
		}
		out = append(out, rf)
	}
	return out
}

// branchPrefix is the path under which a captured discriminator's value is the
// key: plain segments, then one finite matcher, then nothing, as in
// `String(Key("build","kind"), Path("build", Either(ways...)), Captures())`.
func branchPrefix(a *Attribute) ([]string, bool) {
	if !a.Captures || len(a.Path) == 0 {
		return nil, false
	}

	prefix := make([]string, 0, len(a.Path)-1)
	for i, seg := range a.Path {
		if i == len(a.Path)-1 {
			if _, ok := seg.(FiniteMatcher); !ok {
				return nil, false
			}
			return prefix, true
		}
		plain, ok := seg.(string)
		if !ok {
			return nil, false
		}
		prefix = append(prefix, plain)
	}
	return nil, false
}

func attrByName(attrs []*Attribute, name string) *Attribute {
	for _, a := range attrs {
		if a.Name == name {
			return a
		}
	}
	return nil
}

// ValidateField runs one field's single-value validator against value: an
// unknown field (no attribute at that plain path, including a dynamic one) is an
// error; a known field with no validator is nil. The value is first checked
// against the field's declared type.
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
		if !isInteger(value) && !convertible(value, TypeInt) {
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
