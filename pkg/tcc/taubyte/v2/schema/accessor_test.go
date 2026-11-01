package schema_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

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
