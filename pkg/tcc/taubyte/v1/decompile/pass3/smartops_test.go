package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestSmartops_NoSmartops(t *testing.T) {
	smartops := Smartops()

	obj := object.New[object.Refrence]()
	// No smartops group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := smartops.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no smartops")
}

func TestSmartops_WithSmartops(t *testing.T) {
	smartops := Smartops()

	root := object.New[object.Refrence]()
	smartopsObj := object.New[object.Refrence]()

	smartop1 := object.New[object.Refrence]()
	smartop1.Set("name", "my-smartop")
	smartop1.Set("id", "smartop-id-1")
	smartop1.Set("timeout", 10)        // integer
	smartop1.Set("memory", 1073741824) // 1GB in bytes (integer)
	smartop1.Set("secure", true)
	smartop1.Set("method", "GET")
	smartop1.Set("domains", []string{"domain1"})
	err := smartopsObj.Child("smartop-id-1").Add(smartop1)
	assert.NilError(t, err)

	err = root.Child("smartops").Add(smartopsObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := smartops.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultSmartops, err := result.Child("smartops").Object()
	assert.NilError(t, err)
	resultSmartop1, err := resultSmartops.Child("my-smartop").Object()
	assert.NilError(t, err)

	// Should have moved attributes
	httpMethod, err := resultSmartop1.GetString("http-method")
	assert.NilError(t, err)
	assert.Equal(t, httpMethod, "GET")

	httpDomains := resultSmartop1.Get("http-domains")
	assert.Equal(t, httpDomains.([]string)[0], "domain1")

	// Secure should be deleted
	_, err = resultSmartop1.GetBool("secure")
	assert.ErrorContains(t, err, "not exist")
}
