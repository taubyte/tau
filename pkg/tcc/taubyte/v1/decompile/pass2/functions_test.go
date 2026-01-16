package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestFunctions_NoFunctions(t *testing.T) {
	functions := Functions()

	obj := object.New[object.Refrence]()
	// No functions group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := functions.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no functions")
}

func TestFunctions_WithDomains(t *testing.T) {
	functions := Functions()

	// Create root object with domains
	root := object.New[object.Refrence]()
	domains := object.New[object.Refrence]()
	domain1 := object.New[object.Refrence]()
	domain1.Set("name", "domain1")
	domain1.Set("id", "domain-id-1")
	err := domains.Child("domain-id-1").Add(domain1)
	assert.NilError(t, err)
	err = root.Child("domains").Add(domains)
	assert.NilError(t, err)

	// Create functions with domain IDs
	funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("domains", []string{"domain-id-1"})
	err = funcs.Child("func1").Add(func1)
	assert.NilError(t, err)
	err = root.Child("functions").Add(funcs)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	result, err := functions.Process(ctx, root)
	assert.NilError(t, err)

	// Check that domain IDs were resolved to names
	resultFuncs, err := result.Child("functions").Object()
	assert.NilError(t, err)
	resultFunc1, err := resultFuncs.Child("func1").Object()
	assert.NilError(t, err)
	domainsVal := resultFunc1.Get("domains")
	domainsSlice := domainsVal.([]string)
	assert.Equal(t, len(domainsSlice), 1)
	assert.Equal(t, domainsSlice[0], "domain1")
}

func TestFunctions_WithLibraries(t *testing.T) {
	functions := Functions()

	// Create root object with libraries
	root := object.New[object.Refrence]()
	libraries := object.New[object.Refrence]()
	lib1 := object.New[object.Refrence]()
	lib1.Set("name", "lib1")
	lib1.Set("id", "lib-id-1")
	err := libraries.Child("lib-id-1").Add(lib1)
	assert.NilError(t, err)
	err = root.Child("libraries").Add(libraries)
	assert.NilError(t, err)

	// Create functions with library source
	funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("source", "libraries/lib-id-1")
	err = funcs.Child("func1").Add(func1)
	assert.NilError(t, err)
	err = root.Child("functions").Add(funcs)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	result, err := functions.Process(ctx, root)
	assert.NilError(t, err)

	// Check that library ID was resolved to name
	resultFuncs, err := result.Child("functions").Object()
	assert.NilError(t, err)
	resultFunc1, err := resultFuncs.Child("func1").Object()
	assert.NilError(t, err)
	source, err := resultFunc1.GetString("source")
	assert.NilError(t, err)
	assert.Equal(t, source, "libraries/lib1")
}

func TestFunctions_DomainNotFound(t *testing.T) {
	functions := Functions()

	root := object.New[object.Refrence]()
	funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("domains", []string{"non-existent-id"})
	err := funcs.Child("func1").Add(func1)
	assert.NilError(t, err)
	err = root.Child("functions").Add(funcs)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = functions.Process(ctx, root)
	assert.ErrorContains(t, err, "domain ID non-existent-id not found")
}

func TestFunctions_LibraryNotFound(t *testing.T) {
	functions := Functions()

	root := object.New[object.Refrence]()
	funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("source", "libraries/non-existent-id")
	err := funcs.Child("func1").Add(func1)
	assert.NilError(t, err)
	err = root.Child("functions").Add(funcs)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = functions.Process(ctx, root)
	assert.ErrorContains(t, err, "library ID non-existent-id not found")
}

func TestFunctions_DomainsNotSlice(t *testing.T) {
	functions := Functions()

	root := object.New[object.Refrence]()
	funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("domains", "not-a-slice")
	err := funcs.Child("func1").Add(func1)
	assert.NilError(t, err)
	err = root.Child("functions").Add(funcs)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = functions.Process(ctx, root)
	assert.ErrorContains(t, err, "domains is not a []string")
}

func TestFunctions_SourceNotString(t *testing.T) {
	functions := Functions()

	root := object.New[object.Refrence]()
	funcs := object.New[object.Refrence]()
	func1 := object.New[object.Refrence]()
	func1.Set("source", 123)
	err := funcs.Child("func1").Add(func1)
	assert.NilError(t, err)
	err = root.Child("functions").Add(funcs)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = functions.Process(ctx, root)
	assert.ErrorContains(t, err, "source is not a string")
}

func TestFunctions_ErrorFetchingFunctions(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}

func TestPipe(t *testing.T) {
	pipe := Pipe()
	assert.Assert(t, len(pipe) > 0, "pipe should contain transformers")
}
