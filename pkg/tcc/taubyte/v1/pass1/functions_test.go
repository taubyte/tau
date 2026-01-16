package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestFunctions_HTTPTrigger(t *testing.T) {
	// Setup: Create function config with HTTP trigger
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("myFunction")
	funcSel.Set("id", "func-id-123")
	funcSel.Set("type", "http")
	funcSel.Set("timeout", "5s")
	funcSel.Set("memory", "128MB")
	funcSel.Set("http-method", "GET")
	funcSel.Set("http-domains", []string{"domain1"})
	funcSel.Set("http-paths", []string{"/api"})

	// Execute: Run transformer
	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	// Verify: Check transformations
	assert.NilError(t, err)

	// Verify function renamed by ID
	renamedFuncSel := funcsObj.Child("func-id-123")

	// Verify timeout converted to nanoseconds (5s = 5000000000ns)
	timeout, err := renamedFuncSel.Get("timeout")
	assert.NilError(t, err)
	assert.Equal(t, timeout.(int64), int64(5000000000))

	// Verify memory converted to bytes (128MB = 128000000 bytes in decimal)
	memory, err := renamedFuncSel.Get("memory")
	assert.NilError(t, err)
	assert.Equal(t, memory.(int64), int64(128000000))

	// Verify secure flag set to false for HTTP
	secure, err := renamedFuncSel.Get("secure")
	assert.NilError(t, err)
	assert.Equal(t, secure.(bool), false)

	// Verify attributes moved
	_, err = renamedFuncSel.Get("method")
	assert.NilError(t, err)

	_, err = renamedFuncSel.Get("domains")
	assert.NilError(t, err)

	_, err = renamedFuncSel.Get("paths")
	assert.NilError(t, err)

	// Verify name set
	name, err := renamedFuncSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myFunction")

	// Verify indexed
	indexPath := "functions/myFunction"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "func-id-123")

}

func TestFunctions_HTTPSTrigger(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("secureFunction")
	funcSel.Set("id", "func-secure-456")
	funcSel.Set("type", "https")
	funcSel.Set("timeout", "10s")
	funcSel.Set("memory", "256MB")

	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedFuncSel := funcsObj.Child("func-secure-456")

	// Verify secure flag set to true for HTTPS
	secure, err := renamedFuncSel.Get("secure")
	assert.NilError(t, err)
	assert.Equal(t, secure.(bool), true)
}

func TestFunctions_P2PTrigger(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("p2pFunction")
	funcSel.Set("id", "func-p2p-789")
	funcSel.Set("type", "p2p")
	funcSel.Set("p2p-protocol", "test-protocol")
	funcSel.Set("p2p-command", "test-command")
	funcSel.Set("timeout", "10s")  // Set timeout to avoid nil access
	funcSel.Set("memory", "128MB") // Set memory to avoid nil access

	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedFuncSel := funcsObj.Child("func-p2p-789")

	// Verify p2p-protocol moved to service
	_, err = renamedFuncSel.Get("service")
	assert.NilError(t, err)

	// Verify p2p-command moved to command
	_, err = renamedFuncSel.Get("command")
	assert.NilError(t, err)
}

func TestFunctions_PubSubTrigger(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("pubsubFunction")
	funcSel.Set("id", "func-pubsub-101")
	funcSel.Set("type", "pubsub")
	funcSel.Set("pubsub-channel", "test-channel")
	funcSel.Set("timeout", "10s")  // Set timeout to avoid nil access
	funcSel.Set("memory", "128MB") // Set memory to avoid nil access

	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	renamedFuncSel := funcsObj.Child("func-pubsub-101")

	// Verify pubsub-channel moved to channel
	_, err = renamedFuncSel.Get("channel")
	assert.NilError(t, err)
}

func TestFunctions_NoFunctions(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestFunctions_MultipleFunctions(t *testing.T) {
	obj := object.New[object.Refrence]()
	funcsObj, _ := obj.CreatePath("functions")

	// Create multiple functions
	func1 := funcsObj.Child("function1")
	func1.Set("id", "id1")
	func1.Set("type", "http")
	func1.Set("timeout", "10s")  // Set timeout to avoid nil access
	func1.Set("memory", "128MB") // Set memory to avoid nil access

	func2 := funcsObj.Child("function2")
	func2.Set("id", "id2")
	func2.Set("type", "https")
	func2.Set("timeout", "15s")
	func2.Set("memory", "256MB") // Set memory to avoid nil access

	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both functions renamed
	_, err = funcsObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = funcsObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("functions/function1").Exist())
	assert.Assert(t, ctx.Store().String("functions/function2").Exist())
}
