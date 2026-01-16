package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestSmartops_WithTimeoutAndMemory(t *testing.T) {
	obj := object.New[object.Refrence]()
	smartopsObj, _ := obj.CreatePath("smartops")
	smartopSel := smartopsObj.Child("mySmartop")
	smartopSel.Set("id", "smartop-id-123")
	smartopSel.Set("timeout", "30s")
	smartopSel.Set("memory", "512MB")
	smartopSel.Set("call", "main")

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify smartop renamed by ID
	renamedSmartopSel := smartopsObj.Child("smartop-id-123")

	// Verify timeout converted to nanoseconds
	timeout, err := renamedSmartopSel.Get("timeout")
	assert.NilError(t, err)
	assert.Equal(t, timeout.(int64), int64(30000000000))

	// Verify memory converted to bytes (512MB = 512000000 bytes in decimal)
	memory, err := renamedSmartopSel.Get("memory")
	assert.NilError(t, err)
	assert.Equal(t, memory.(int64), int64(512000000))

	// Verify name set
	name, err := renamedSmartopSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "mySmartop")

	// Verify indexed
	indexPath := "smartops/mySmartop"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "smartop-id-123")

}

func TestSmartops_WithHTTPSecure(t *testing.T) {
	obj := object.New[object.Refrence]()
	smartopsObj, _ := obj.CreatePath("smartops")
	smartopSel := smartopsObj.Child("httpSmartop")
	smartopSel.Set("id", "smartop-http-456")
	smartopSel.Set("type", "http")
	smartopSel.Set("timeout", "10s")  // Set timeout to avoid nil access
	smartopSel.Set("memory", "128MB") // Set memory to avoid nil access

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedSmartopSel := smartopsObj.Child("smartop-http-456")

	// Verify secure flag set to false for HTTP
	secure, err := renamedSmartopSel.Get("secure")
	assert.NilError(t, err)
	assert.Equal(t, secure.(bool), false)
}

func TestSmartops_WithHTTPSSecure(t *testing.T) {
	obj := object.New[object.Refrence]()
	smartopsObj, _ := obj.CreatePath("smartops")
	smartopSel := smartopsObj.Child("httpsSmartop")
	smartopSel.Set("id", "smartop-https-789")
	smartopSel.Set("type", "https")
	smartopSel.Set("timeout", "10s")  // Set timeout to avoid nil access
	smartopSel.Set("memory", "128MB") // Set memory to avoid nil access

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedSmartopSel := smartopsObj.Child("smartop-https-789")

	// Verify secure flag set to true for HTTPS
	secure, err := renamedSmartopSel.Get("secure")
	assert.NilError(t, err)
	assert.Equal(t, secure.(bool), true)
}

func TestSmartops_NoSmartops(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestSmartops_MultipleSmartops(t *testing.T) {
	obj := object.New[object.Refrence]()
	smartopsObj, _ := obj.CreatePath("smartops")

	smartop1 := smartopsObj.Child("smartop1")
	smartop1.Set("id", "id1")
	smartop1.Set("timeout", "10s")
	smartop1.Set("memory", "128MB") // Set memory to avoid nil access

	smartop2 := smartopsObj.Child("smartop2")
	smartop2.Set("id", "id2")
	smartop2.Set("memory", "64MB")
	smartop2.Set("timeout", "5s") // Set timeout to avoid nil access

	transformer := Smartops()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both smartops renamed
	_, err = smartopsObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = smartopsObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("smartops/smartop1").Exist())
	assert.Assert(t, ctx.Store().String("smartops/smartop2").Exist())
}
