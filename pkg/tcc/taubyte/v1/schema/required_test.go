package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// A RequiredWhen that names a sibling which doesn't exist, or a value the
// discriminator can never hold, fires nowhere and reports nothing: the load path
// returns false, RequiredFields skips it, and the emitted schema still advertises
// the condition. Nothing else in the build catches that, so this does.
func TestRequiredWhenDeclarationsAreSound(t *testing.T) {
	root := GenerationRoot()
	for _, g := range root {
		group, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		attrs := g.Children[0].Attributes
		for _, a := range attrs {
			cond, ok := a.Meta["requiredWhen"].(engine.ConditionSpec)
			if !ok {
				continue
			}
			where := group + "." + a.Name
			if _, both := a.Meta["requiredUnless"].(string); both {
				t.Errorf("%s declares both RequiredWhen and RequiredUnless; one would silently win", where)
			}

			// (1) the discriminator must be an attribute of the same resource
			var sib *engine.Attribute
			for _, c := range attrs {
				if c.Name == cond.Field {
					sib = c
					break
				}
			}
			assert.Assert(t, sib != nil,
				"%s: RequiredWhen names %q, which %s has no attribute for", where, cond.Field, group)

			// (2) it must be addressable, or the condition can never be evaluated
			assert.Assert(t, len(cond.In) > 0, "%s: RequiredWhen lists no values", where)

			// (3) every triggering value must be one the discriminator can hold,
			// or the requirement is unreachable (a typo'd "pubusb" fires never)
			enum, hasEnum := sib.Meta["enum"].([]string)
			if !hasEnum {
				continue // free-form discriminator: any value is possible
			}
			for _, v := range cond.In {
				assert.Assert(t, slices.Contains(enum, v),
					"%s: RequiredWhen expects %s=%q, but %q only admits %s",
					where, cond.Field, v, cond.Field, strings.Join(enum, "|"))
			}
		}
	}
}

// A RequiredUnless whose sibling is not a bool reads as "not required" on one
// path and "required" on the other — the evaluator asserts a bool on both sides.
// A sibling that does not exist fires nowhere at all.
func TestRequiredUnlessDeclarationsAreSound(t *testing.T) {
	root := GenerationRoot()
	for _, g := range root {
		group, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		attrs := g.Children[0].Attributes
		for _, a := range attrs {
			field, ok := a.Meta["requiredUnless"].(string)
			if !ok {
				continue
			}
			where := group + "." + a.Name
			var sib *engine.Attribute
			for _, c := range attrs {
				if c.Name == field {
					sib = c
					break
				}
			}
			assert.Assert(t, sib != nil, "%s: RequiredUnless names %q, which %s has no attribute for", where, field, group)
			assert.Equal(t, sib.Type, engine.TypeBool,
				"%s: RequiredUnless(%q) needs a boolean discriminator, got %v", where, field, sib.Type)
			assert.Assert(t, engine.RequiredPathOf(sib) != "",
				"%s: the discriminator %q must be addressable", where, field)
		}
	}
}

// Required-ness must be expressible: a field at a dynamic (Either/Key) path has
// no plain authored path, so RequiredFields silently drops it and only the load
// path would enforce it. Declaring one is a mistake worth catching here.
func TestRequiredFieldsAreAddressable(t *testing.T) {
	root := GenerationRoot()
	for _, g := range root {
		group, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		reported := map[string]bool{}
		for _, rf := range engine.RequiredFields(root, group) {
			reported[strings.Join(rf.Path, "/")] = true
		}
		for _, a := range g.Children[0].Attributes {
			_, conditional := a.Meta["requiredWhen"].(engine.ConditionSpec)
			if !a.Required && !conditional {
				continue
			}
			if a.Key {
				continue // a map key is required by construction (empty key errors)
			}
			path := engine.RequiredPathOf(a)
			assert.Assert(t, path != "",
				"%s.%s is required at a dynamic path, so partial validation cannot report it",
				group, a.Name)
			assert.Assert(t, reported[path],
				"%s.%s is required but RequiredFields does not report it", group, a.Name)
		}
	}
}
