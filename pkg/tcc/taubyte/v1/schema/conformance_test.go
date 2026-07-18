package schema

import (
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"gotest.tools/v3/assert"
)

// TestEnumBoolDerivedBoolDomains makes the EnumBool/DerivedBool declarations
// self-checking: every compile/decompile value they name must be a member of the
// source attribute's InSet domain. A stray value (a decompileAs/trueWhen/dropWhen
// entry the enum can never hold, or a When key outside the source domain) would
// otherwise compile fine yet produce an unreachable branch in the future generic
// driver; this catches it at the declaration site instead.
func TestEnumBoolDerivedBoolDomains(t *testing.T) {
	seen := 0
	for _, g := range GenerationRoot() {
		group, _ := g.Match.(string)
		if len(g.Children) == 0 {
			continue
		}
		for _, a := range g.Children[0].Attributes {
			domain, hasEnum := a.Meta["enum"].([]string)
			set := map[string]bool{}
			for _, v := range domain {
				set[v] = true
			}
			where := group + "." + a.Name

			if eb, ok := a.Meta["enumBool"].(engine.EnumBoolSpec); ok {
				seen++
				assert.Assert(t, hasEnum, "%s: EnumBool(%q) has no InSet domain to check against", where, eb.GoName)
				for _, v := range eb.DecompileAs {
					assert.Assert(t, set[v], "%s: EnumBool decompileAs %q is not in InSet domain %v", where, v, domain)
				}
				for _, v := range eb.TrueWhen {
					assert.Assert(t, set[v], "%s: EnumBool trueWhen %q is not in InSet domain %v", where, v, domain)
				}
				for _, v := range eb.DropWhen {
					assert.Assert(t, set[v], "%s: EnumBool dropWhen %q is not in InSet domain %v", where, v, domain)
				}
			}

			if db, ok := a.Meta["derivedBool"].(engine.DerivedBoolSpec); ok {
				seen++
				assert.Assert(t, hasEnum, "%s: DerivedBool(%q) has no InSet domain to check against", where, db.GoName)
				for v := range db.When {
					assert.Assert(t, set[v], "%s: DerivedBool when-key %q is not in InSet domain %v", where, v, domain)
				}
			}
		}
	}
	// Guard the guard: if the schema stops declaring any Enum/DerivedBool, this
	// test would pass vacuously — fail loudly so it can't rot into a no-op.
	assert.Assert(t, seen > 0, "expected at least one EnumBool/DerivedBool in the schema")
}
