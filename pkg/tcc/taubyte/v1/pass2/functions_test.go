package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestFunctions_ResolveDomainNames(t *testing.T) {
	// Setup: Create domains and functions with domain names
	obj := object.New[object.Refrence]()

	// Create domains first (as pass1 would have done)
	domainsObj, _ := obj.CreatePath("domains")
	domain1Sel := domainsObj.Child("domain1")
	domain1Sel.Set("id", "domain-id-1")
	domain2Sel := domainsObj.Child("domain2")
	domain2Sel.Set("id", "domain-id-2")

	// Create functions with domain names (not IDs)
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-id-123")
	funcSel.Set("domains", []string{"domain1", "domain2"})

	// Index domains (as pass1 would have done)
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	err := utils.IndexById(ctx, "domains", "domain1", "domain-id-1")
	assert.NilError(t, err)
	err = utils.IndexById(ctx, "domains", "domain2", "domain-id-2")
	assert.NilError(t, err)

	// Execute: Run pass2 transformer
	transformer := Functions()
	_, err = transformer.Process(ctx, obj)

	// Verify: Domain names resolved to IDs
	assert.NilError(t, err)

	resolvedDomains, err := funcsObj.Child("func-id-123").Get("domains")
	assert.NilError(t, err)
	domainIds := resolvedDomains.([]string)
	assert.Equal(t, len(domainIds), 2)
	assert.Equal(t, domainIds[0], "domain-id-1")
	assert.Equal(t, domainIds[1], "domain-id-2")

}

func TestFunctions_ResolveLibrarySource(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Create libraries first
	librariesObj, _ := obj.CreatePath("libraries")
	libSel := librariesObj.Child("mylib")
	libSel.Set("id", "lib-id-456")

	// Create function with library source name
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-id-789")
	funcSel.Set("source", "libraries/mylib")

	// Index library
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	err := utils.IndexById(ctx, "libraries", "mylib", "lib-id-456")
	assert.NilError(t, err)

	// Execute: Run pass2 transformer
	transformer := Functions()
	_, err = transformer.Process(ctx, obj)

	// Verify: Library name resolved to ID
	assert.NilError(t, err)

	source, err := funcsObj.Child("func-id-789").Get("source")
	assert.NilError(t, err)
	assert.Equal(t, source.(string), "libraries/lib-id-456")

}

func TestFunctions_NoFunctions(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Functions()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestFunctions_WithBothDomainAndLibrary(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Setup domains
	domainsObj, _ := obj.CreatePath("domains")
	domainSel := domainsObj.Child("mydomain")
	domainSel.Set("id", "domain-id-999")

	// Setup libraries
	librariesObj, _ := obj.CreatePath("libraries")
	libSel := librariesObj.Child("mylib")
	libSel.Set("id", "lib-id-999")

	// Setup function
	funcsObj, _ := obj.CreatePath("functions")
	funcSel := funcsObj.Child("func-id-999")
	funcSel.Set("domains", []string{"mydomain"})
	funcSel.Set("source", "libraries/mylib")

	// Index resources
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	utils.IndexById(ctx, "domains", "mydomain", "domain-id-999")
	utils.IndexById(ctx, "libraries", "mylib", "lib-id-999")

	// Execute
	transformer := Functions()
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both resolved
	domains, _ := funcsObj.Child("func-id-999").Get("domains")
	assert.Equal(t, domains.([]string)[0], "domain-id-999")

	source, _ := funcsObj.Child("func-id-999").Get("source")
	assert.Equal(t, source.(string), "libraries/lib-id-999")
}
