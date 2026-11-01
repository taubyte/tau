package schema_test

import (
	"context"
	"strings"
	"testing"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"gotest.tools/v3/assert"
)

// The fixture holds three caches called `sessions` (shop, billing, shop/local), so
// the scope has to be in the key; the nested one checks scope beyond one level.
func compileIndex(t *testing.T) (map[string]any, string) {
	t.Helper()
	c, err := schema.New(schema.WithLocal("../fixtures/basic"))
	assert.NilError(t, err)
	obj, _, err := c.Compile(context.Background())
	assert.NilError(t, err)

	flat := obj.Flat()
	idx, ok := flat["indexes"].(map[string]any)
	assert.Assert(t, ok, "the compiled object carries no index")
	project, _ := flat["object"].(map[string]any)["id"].(string)
	assert.Assert(t, project != "")
	return idx, project
}

// links is the single entry a fresh compile puts in a bucket.
func link(t *testing.T, idx map[string]any, key string) string {
	t.Helper()
	got, ok := idx[key].([]string)
	assert.Assert(t, ok, "no index entry at %s", key)
	assert.Equal(t, len(got), 1)
	return got[0]
}

func TestIndexResolvesANameInItsOwnScope(t *testing.T) {
	idx, project := compileIndex(t)

	shop := link(t, idx, "projects/"+project+"/namespace/shop/links")
	billing := link(t, idx, "projects/"+project+"/namespace/billing/links")
	assert.Assert(t, shop != billing)

	// a namespace resolves to its id the same way anything else does, which is what
	// turns the written `/shop/local` into the segments every other key is built from
	shopID := shop[strings.LastIndex(shop, "/")+1:]
	local := link(t, idx, "projects/"+project+"/namespace/"+shopID+"/namespace/local/links")
	euID := local[strings.LastIndex(local, "/")+1:]
	assert.Assert(t, euID != shopID)

	billingID := billing[strings.LastIndex(billing, "/")+1:]

	// the three caches, and the three ids they answer with are three ids
	root := link(t, idx, "projects/"+project+"/cache/orders/links")
	inShop := link(t, idx, "projects/"+project+"/namespace/"+shopID+"/cache/sessions/links")
	inBilling := link(t, idx, "projects/"+project+"/namespace/"+billingID+"/cache/sessions/links")
	inEU := link(t, idx, "projects/"+project+"/namespace/"+shopID+"/namespace/"+euID+"/cache/sessions/links")

	seen := map[string]bool{}
	for _, v := range []string{root, inShop, inBilling, inEU} {
		assert.Assert(t, !seen[v], "%s is indexed twice", v)
		seen[v] = true
	}

	// the value is where the resource actually is, ids all the way down
	assert.Assert(t, strings.HasPrefix(inEU,
		"branches/main/projects/"+project+"/namespace/"+shopID+"/namespace/"+euID+"/cache/"),
		"the nested cache is indexed at %s", inEU)
}

func TestIndexCoversWhatALoweringProduced(t *testing.T) {
	idx, project := compileIndex(t)

	// The scope is indexed by name under the source kind: workflow is both the
	// resource and the scope its queues live in.
	checkout := link(t, idx, "projects/"+project+"/workflow/checkout/links")
	nsID := checkout[strings.LastIndex(checkout, "/")+1:]
	assert.Assert(t, nsID != "")
	_, asNamespace := idx["projects/"+project+"/namespace/checkout/links"]
	assert.Assert(t, !asNamespace, "the scope a workflow became is indexed as a namespace")

	for _, name := range []string{"checkout_reserve", "checkout_charge", "checkout_failed"} {
		value := link(t, idx, "projects/"+project+"/workflow/"+nsID+"/sync/"+name+"/links")
		assert.Assert(t, strings.HasPrefix(value,
			"branches/main/projects/"+project+"/workflow/"+nsID+"/sync/"),
			"%s is indexed at %s", name, value)
	}

	// and NOT at the project root, where the queues somebody wrote are
	_, atRoot := idx["projects/"+project+"/sync/checkout_reserve/links"]
	assert.Assert(t, !atRoot, "a produced queue is still indexed beside the authored ones")
	link(t, idx, "projects/"+project+"/sync/orders/links")
}
