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

// FieldRef is where a field may be authored.
type FieldRef struct {
	Path []string
	// AnyOf is every plain location the field may be authored at, when its
	// declared path contains a FINITE matcher (Either). A storage's size lives
	// at object/size or streaming/size depending on which block the author
	// wrote — two static locations, not a dynamic one. AnyOf[0] == Path; nil
	// when the path has no matcher.
	AnyOf [][]string
}

// RequiredField is a field whose absence the compiler rejects, and the condition
// under which that applies. When is nil for an unconditional Required(); for a
// RequiredWhen it names the sibling that decides, and the values that trigger it.
type RequiredField struct {
	FieldRef
	When   *FieldRef // the discriminator to read, nil when unconditional
	WhenIn []string  // ...required only if its value is one of these
	// Unless inverts the sense AND the default: the field is required unless
	// the discriminator (a bool) is true, and an ABSENT discriminator means
	// required — an unset bool is false. RequiredWhen cannot express that:
	// there, an absent discriminator means NOT required.
	Unless bool

	// WhenBranchAt is set when the discriminator has NO value of its own: its
	// path ends in a finite matcher and it Captures(), which means the value IS
	// THE KEY an author wrote under this prefix.
	//
	//	build:                 # WhenBranchAt = ["build"]
	//	    commands:          # ...and the discriminator's value is "commands"
	//	        image: node:22
	//
	// WITHOUT THIS, EVERY REQUIRED FIELD ON A BRANCH WAS SILENTLY DROPPED. The
	// discriminator's own path is dynamic, so reading it as a field returned
	// nothing and the condition could never be true — which took `build/image`,
	// `build/run`, `build/script` and `build/dockerfile` out of the required
	// set entirely. An importer emitting a build block therefore produced a
	// project that would not compile and a to-do list that named nothing.
	//
	// When WhenBranch is true, When is nil: there is nothing to read as a
	// field. WhenBranchAt may be EMPTY and that is meaningful — the branch key
	// sits at the resource's own root, which is where a stream's engine and a
	// gateway's routing live. A caller testing len(WhenBranchAt) instead of
	// this bool makes every one of those unconditionally required.
	WhenBranch   bool
	WhenBranchAt []string
}

// fieldPaths is every plain authored location of an attribute: a static path
// yields one, and each FINITE matcher segment multiplies into its values. An
// unbounded matcher (All) yields nil — such a field has no enumerable location
// and stays unaddressable, which is what RequiredPathOf reports.
//
// This is the whole trick behind required fields at a "dynamic" path: Either is
// declared, so the set of locations is known statically. Nothing has to resolve
// a matcher against a document outside the engine.
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
			unlessField, unless := a.Meta["requiredUnless"].(string)
			if !a.Required && !conditional && !unless {
				continue
			}
			paths := fieldPaths(a)
			if paths == nil {
				continue
			}
			rf := RequiredField{FieldRef: FieldRef{Path: paths[0]}}
			if len(paths) > 1 {
				rf.AnyOf = paths
			}
			if unless {
				sib := attrByName(attrs, unlessField)
				if sib == nil {
					continue // names an attribute this group doesn't have
				}
				sp := fieldPath(sib)
				if sp == nil {
					continue
				}
				rf.When = &FieldRef{Path: sp}
				rf.Unless = true
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
					rf.When = &FieldRef{Path: sp}
				} else if at, ok := branchPrefix(sib); ok {
					// A CAPTURED BRANCH: the discriminator has no value of its
					// own, its value is the key under this prefix.
					rf.WhenBranch, rf.WhenBranchAt = true, at
				} else {
					continue // a dynamic path that is not a captured branch
				}
			}
			out = append(out, rf)
		}
	}
	return out
}

// branchPrefix is the path under which a CAPTURED discriminator's value is the
// key, and whether the attribute is one.
//
// It is exactly the shape `String(Key("build","kind"), Path("build",
// Either(ways...)), Captures())` has: plain segments, then one finite matcher,
// and nothing after it. Anything else is a dynamic path this cannot resolve —
// a matcher in the middle would make the prefix itself depend on a branch, and
// a non-finite one enumerates nothing.
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

// RequiredPathOf is an attribute's authored path as a display/lookup key, or ""
// when it sits at a dynamic (Either/Key) location with no plain path.
func RequiredPathOf(a *Attribute) string {
	if p := fieldPaths(a); p != nil {
		return strings.Join(p[0], "/")
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
