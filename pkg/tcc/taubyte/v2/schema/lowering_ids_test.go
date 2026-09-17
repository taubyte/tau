package schema_test

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// A DERIVED ID IS HUNG OFF THE LOWERING'S NAME AS DECLARED, so how a name is WRITTEN is a
// stored fact and not a matter of taste.
//
// A resource a compiler wrote is stored under its id and referred to by it. The id of one
// produced per resource — rather than per element of a list, which has the element's own
// name to hang off — comes from the source's id, the kind, and the lowering's name term
// PRINTED. So changing how a name is spelled re-keys every resource it produced: every
// build after it looks like a delete and a create, and two clouds compiling the same
// project disagree about what is the same resource.
//
// This test says the terms did not do that. The right-hand side is the literal string
// template that stood in definition.go before names became terms — `"{name}_failed"`,
// `"{name}_infer"`, `"{name}_memory"` — so it passing is the statement that
// `Concat(Attr("name"), Text("_failed"))` still keys exactly what the string did.
//
// It is also why Term.String() keeps the old `{…}` spelling. That is not nostalgia: it is
// load-bearing, and this is the test that makes it so.
func TestALoweringsIdsSurvivedTheMoveToTerms(t *testing.T) {
	c, err := schema.New(schema.WithLocal("../fixtures/basic"), schema.WithBranch("main"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	asWritten := map[string]string{
		"sync/checkout_failed":    "{name}_failed",
		"function/support_infer":  "{name}_infer",
		"database/support_memory": "{name}_memory",
	}

	seen := 0
	for _, d := range schema.LoweringPlan(obj.Flat()) {
		old, ours := asWritten[d.Kind+"/"+d.Name]
		if !ours {
			continue
		}
		want, err := engine.Derive(engine.DeriveResourceID, d.Source.ID, d.Kind, old)
		assert.NilError(t, err)
		assert.Equal(t, d.ID, want, "%s/%s is stored under a different id than it used to be", d.Kind, d.Name)
		seen++
	}
	assert.Equal(t, seen, len(asWritten), "not every lowering that produces one per resource was checked")
}
