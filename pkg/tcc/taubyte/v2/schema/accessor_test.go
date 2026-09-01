package schema_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// A GENERATED CLIENT ADDRESSES A FIELD BY THE LAST SEGMENT OF THE PATH IT READS, so two
// attributes of one kind whose declared paths end in the same word are one field, and
// the generator keeps the first and drops the rest — silently, because a dropped field
// is a name that is simply not in the emitted client. `kv/distributed/size`,
// `document/distributed/size`, `sql/distributed/size` and `vector/distributed/size` were
// four such attributes and three were unreachable; `consensus` was about to be the
// second set of four, and a fifth data model would have been the third.
//
// THIS IS A PROPERTY OF THE VOCABULARY, NOT A BUG IN THE GENERATOR. Naming a field after
// its path leaf is a reasonable rule, and it is this schema that hands the generator four
// leaves spelled the same. The lever is `Accessor(...)`, which is what it exists for: the
// exported name when the derived one is wrong. The type is the discriminator here, so
// `KvSize` and `SqlSize` say which of the four each one is.
//
// WHY THE WHOLE VOCABULARY AND NOT THOSE EIGHT. Declaring a field once inside a per-type
// loop is the shape that makes this likely rather than unlikely: every attribute the loop
// adds arrives four times with one leaf, and a fifth type multiplies whatever is already
// there. A test over the eight would have passed the day somebody added the ninth.
//
// WHAT IT DOES NOT COVER, deliberately: an attribute that declares no `Path()` of its
// own. The engine derives that path from the `Key()`, so the path RESTATES the key and
// the generator has the full nested spelling available to name it by — which is the
// generator's own naming rule to apply and not this file's to assert. Those are excluded
// by comparing the path against the attribute's name.
func TestNoTwoAttributesCollapseToOneAccessor(t *testing.T) {
	for _, g := range schema.V2Resources {
		if len(g.Children) == 0 {
			continue
		}
		byName := map[string][]string{}
		for _, a := range g.Children[0].Attributes {
			// A Key() attribute has no value at its own path — it holds the branch
			// that matched — so nothing is generated to address.
			if a.Captures {
				continue
			}
			name, ok := addressableName(a)
			if !ok {
				continue
			}
			byName[name] = append(byName[name], a.Name)
		}

		names := make([]string, 0, len(byName))
		for n := range byName {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if len(byName[n]) > 1 {
				t.Errorf("%s: %s all address the one field %q, so a generated client keeps the first and drops the rest — give each an Accessor(...) that says which it is",
					g.Match, strings.Join(byName[n], ", "), n)
			}
		}
	}
}

// addressableName is the field a generated client would name this attribute: the
// Accessor() override if there is one, else the last segment of a path this schema
// DECLARED. ok is false when there is nothing to compare — a path with a matcher in it
// has no single location and is not mechanically emitted, and a path the engine derived
// from the key is named by the key instead (see the note above the test).
func addressableName(a *engine.Attribute) (string, bool) {
	if ov, ok := a.Meta["accessor"].(string); ok && ov != "" {
		return ov, true
	}
	segs := make([]string, 0, len(a.Path))
	for _, p := range a.Path {
		s, isStr := p.(string)
		if !isStr {
			return "", false
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 || strings.Join(segs, "/") == a.Name {
		return "", false
	}
	return segs[len(segs)-1], true
}
