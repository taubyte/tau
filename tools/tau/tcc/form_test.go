package tcc

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// THE DSL IS THE SOURCE OF TRUTH, AND THAT HAS TO BE TRUE OF THE TESTS TOO.
//
// The first version of this file said exactly that and then hardcoded
// `functions`, `trigger/method` and a kind called `Messaging` — names the
// vocabulary has since changed or dropped. Every one of them broke, and what
// broke was the tests: the driver reads the schema and was right the whole
// time.
//
// So nothing below names a kind, a field or an enum member. Each test finds
// what it needs in the live schema and asserts a PROPERTY of it — which is
// stronger as well as more durable, because "every kind's flags are unique"
// covers kinds nobody thought to write a case for.

// forms is every kind the CLI exposes, built.
func forms(t *testing.T) map[string]*Form {
	t.Helper()

	groups, err := Groups()
	assert.NilError(t, err)
	assert.Assert(t, len(groups) > 0, "the vocabulary exposes no kinds at all")

	out := map[string]*Form{}
	for _, g := range groups {
		f, err := FormFor(g.Def)
		assert.NilError(t, err, "no form for %s", g.Def)
		out[g.Def] = f
	}
	return out
}

// TestEveryGroupIsAddressable. A group the CLI lists and cannot build a form
// for is a command that exists and does not work.
func TestEveryGroupIsAddressable(t *testing.T) {
	groups, err := Groups()
	assert.NilError(t, err)

	containers := 0
	for _, g := range groups {
		assert.Assert(t, g.Dir != "", "a group with no directory")
		assert.Assert(t, g.Def != "", "%s names no schema definition", g.Dir)
		assert.Equal(t, g.Name, strings.ToLower(g.Def),
			"the command name is the definition lowercased and nothing else")

		found, err := GroupFor(g.Dir)
		assert.NilError(t, err, g.Dir)
		assert.Equal(t, found.Def, g.Def)

		if g.Container {
			containers++
		}
	}

	// THERE IS EXACTLY ONE CONTAINER KIND, and the CLI's "inside a scope"
	// behaviour is written against that. Two would make `tau select` ambiguous
	// and none would make it dead code.
	assert.Equal(t, containers, 1, "expected one container kind, got %d", containers)
}

func TestGroupForUnknown(t *testing.T) {
	_, err := GroupFor("nonesuch")
	assert.ErrorContains(t, err, "no resource kind")
}

func TestFormForUnknown(t *testing.T) {
	_, err := FormFor("Nope")
	assert.ErrorContains(t, err, "no schema definition")
}

// TestIdIsReadOnlyAndNameIsNotEdited, on every kind that has them. An id is
// minted and a name is the resource's address, so neither is a field somebody
// fills in.
func TestIdIsReadOnlyAndNameIsNotEdited(t *testing.T) {
	seen := 0
	for def, f := range forms(t) {
		for _, fd := range f.Fields {
			path := join(fd.Path)
			assert.Assert(t, path != "name", "%s offers `name` as an editable field", def)
			if path == "id" {
				assert.Equal(t, fd.Widget, WidgetCID, "%s's id is not read-only", def)
				seen++
			}
		}
	}
	assert.Assert(t, seen > 0, "no kind carries an id, which cannot be right")
}

// TestNoKindsFlagsCollide. Two fields with the same leaf key get their parent
// segment as a qualifier; without it one of them silently shadows the other on
// the command line.
//
// It runs over EVERY kind rather than the one that happened to have a
// collision when this was written — which is how the kind that had one stopped
// being covered when it was renamed.
func TestNoKindsFlagsCollide(t *testing.T) {
	for def, f := range forms(t) {
		flags := map[string]int{}
		for _, fd := range f.Fields {
			assert.Assert(t, fd.Flag != "", "%s has a field with no flag: %s", def, join(fd.Path))
			flags[fd.Flag]++
		}
		for flag, n := range flags {
			assert.Equal(t, n, 1, "%s assigns flag %q %d times", def, flag, n)
		}
	}
}

// TestEveryWidgetIsWellFormed — whatever the schema says a field is, the form
// carries what that widget needs to be usable.
func TestEveryWidgetIsWellFormed(t *testing.T) {
	groups, err := Groups()
	assert.NilError(t, err)

	dirs := map[string]bool{}
	for _, g := range groups {
		dirs[g.Dir] = true
	}

	seen := map[Widget]int{}
	for def, f := range forms(t) {
		for _, fd := range f.Fields {
			seen[fd.Widget]++
			where := def + "." + join(fd.Path)

			switch fd.Widget {
			case WidgetSelect:
				assert.Assert(t, len(fd.Enum) > 0, "%s is a select with no members", where)
			case WidgetScalar:
				assert.Assert(t, fd.Scalar == "bytes" || fd.Scalar == "duration",
					"%s is a scalar of %q, which is neither a size nor a duration", where, fd.Scalar)
			case WidgetRef, WidgetRefList:
				assert.Assert(t, fd.Ref != nil, "%s is a reference to nothing", where)

				// A REFERENCE EITHER NAMES ITS GROUP OR NAMES THE FIELD THAT
				// DOES. A handler block has no group of its own — the author
				// picks the kind one field earlier — and a schema that said
				// neither was a reference nothing could complete or check.
				if fd.Ref.Group != "" {
					assert.Assert(t, dirs[fd.Ref.Group],
						"%s references %q, which is not a kind this CLI knows", where, fd.Ref.Group)
					continue
				}
				assert.Assert(t, fd.Ref.KindFrom != "",
					"%s references nothing and names no field that decides", where)
				assert.Assert(t, paths(f)[fd.Ref.KindFrom],
					"%s reads its kind from %q, which is not a field of %s", where, fd.Ref.KindFrom, def)
			case WidgetBranchSelect:
				assert.Assert(t, fd.IsSelector, "%s selects a branch and is not a selector", where)
				// ONE ALTERNATIVE IS LEGAL. The vocabulary writes a
				// single-branch Either deliberately where a second is expected
				// later, so that adding it is a line rather than a reshape.
				assert.Assert(t, len(fd.Alternatives) > 0,
					"%s selects a branch and offers none", where)
			}
		}
	}

	// EVERY WIDGET THE DRIVER PRODUCES SHOULD OCCUR SOMEWHERE, or the mapping
	// that produces it is dead. The list is what this vocabulary actually
	// contains, and it is asserted rather than described so that a widget which
	// stops being produced is a failure here instead of a prompt nobody sees.
	//
	// `ref-list` is the one that does not occur: v2 spells "what this reaches"
	// as a keyed block per target and never as a list of names, so nothing maps
	// to it. It stays in the driver because the mapping is one line and the
	// next vocabulary may need it.
	for _, w := range []Widget{
		WidgetCID, WidgetText, WidgetList, WidgetSelect,
		WidgetSwitch, WidgetScalar, WidgetRef, WidgetBranchSelect,
	} {
		assert.Assert(t, seen[w] > 0, "no field anywhere is a %s", w)
	}
}

// paths is every field path in a form, for checking that a cross-reference
// points at one.
func paths(f *Form) map[string]bool {
	out := map[string]bool{}
	for _, fd := range f.Fields {
		out[join(fd.Path)] = true
	}
	return out
}

// TestSectionsAreDeclaredAndOrdered. Every field names a section, and every
// section it names exists — a field pointing at a section nobody declared is a
// prompt that never appears.
func TestEveryFieldsSectionExists(t *testing.T) {
	for def, f := range forms(t) {
		sections := map[string]bool{}
		for _, s := range f.Sections {
			assert.Assert(t, s.ID != "", "%s has a section with no id", def)
			assert.Assert(t, !sections[s.ID], "%s declares section %q twice", def, s.ID)
			sections[s.ID] = true
		}
		for _, fd := range f.Fields {
			if fd.Section == "" {
				continue
			}
			assert.Assert(t, sections[fd.Section],
				"%s.%s is in section %q, which is not declared", def, join(fd.Path), fd.Section)
		}
	}
}

// TestAConditionalSectionNamesAFieldAndValues, wherever the vocabulary declares
// one. A show-when pointing at nothing hides a section forever.
func TestConditionsPointAtSomething(t *testing.T) {
	conditions := 0

	for def, f := range forms(t) {
		paths := map[string]bool{}
		for _, fd := range f.Fields {
			paths[join(fd.Path)] = true
		}

		check := func(what string, c *Condition) {
			if c == nil {
				return
			}
			conditions++
			assert.Assert(t, len(c.Path) > 0, "%s: %s is conditional on nothing", def, what)
			assert.Assert(t, len(c.In) > 0, "%s: %s is conditional on no value", def, what)
		}
		for _, s := range f.Sections {
			check("section "+s.ID, s.ShowWhen)
		}
		for _, fd := range f.Fields {
			check(join(fd.Path), fd.ShowWhen)
		}
	}

	assert.Assert(t, conditions > 0, "nothing in the vocabulary is conditional, which cannot be right")
}

func TestParseDynamic(t *testing.T) {
	prefix, alts, suffix, sel := parseDynamic("{object|streaming}/size")
	assert.DeepEqual(t, prefix, []string{})
	assert.DeepEqual(t, alts, []string{"object", "streaming"})
	assert.DeepEqual(t, suffix, []string{"size"})
	assert.Equal(t, sel, false)

	prefix, alts, _, sel = parseDynamic("source/{github}")
	assert.DeepEqual(t, prefix, []string{"source"})
	assert.DeepEqual(t, alts, []string{"github"})
	assert.Equal(t, sel, true)

	_, _, _, sel = parseDynamic("no/branch/here")
	assert.Equal(t, sel, false)
}

func join(p []string) string { return strings.Join(p, "/") }
