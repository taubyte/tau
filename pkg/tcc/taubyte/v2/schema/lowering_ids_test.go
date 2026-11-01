package schema_test

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/engine"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

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
