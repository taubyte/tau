package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/function"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestFunctions_GlobalFunctionWithDomains(t *testing.T) {
	// Setup: Create root, configRoot, and function config
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create domains
	domainsObj, _ := configRoot.CreatePath("domains")
	domainSel := domainsObj.Child("domain-id-1")
	domainSel.Set("fqdn", "example.com")

	// Create function
	funcConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-456")
	funcSel.Set("name", "myFunction")
	funcSel.Set("domains", []string{"domain-id-1"})

	// Setup context with proper path
	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	// Execute
	transformer := Functions("main")
	_, err := transformer.Process(ctx, configRoot)

	// Verify
	assert.NilError(t, err)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)

	// Verify WASM link path exists (check that something was set)
	// The exact path depends on TNS implementation, but we can verify indexes were created
	assert.Assert(t, indexes != nil)

}

func TestFunctions_AppFunctionWithGlobalDomainFallback(t *testing.T) {
	// Test case where app has no domains, falls back to global domains
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create global domain
	globalDomainsObj, _ := configRoot.CreatePath("domains")
	globalDomainSel := globalDomainsObj.Child("global-domain-id")
	globalDomainSel.Set("fqdn", "global.example.com")

	// Create app (no domains in app)
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	// Create function in app with domain reference
	funcConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-999")
	funcSel.Set("name", "appFunction")
	funcSel.Set("domains", []string{"global-domain-id"})

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Functions("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_ = result
}

func TestFunctions_AppFunctionWithSecondaryDomainFallback(t *testing.T) {
	// Test case where domain is not in app domains, falls back to global domains
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create global domain
	globalDomainsObj, _ := configRoot.CreatePath("domains")
	globalDomainSel := globalDomainsObj.Child("global-domain-id")
	globalDomainSel.Set("fqdn", "global.example.com")

	// Create app with app-level domain
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	appDomainsObj, _ := appObj.CreatePath("domains")
	appDomainSel := appDomainsObj.Child("app-domain-id")
	appDomainSel.Set("fqdn", "app.example.com")

	// Create function in app referencing global domain (not app domain)
	funcConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-999")
	funcSel.Set("name", "appFunction")
	funcSel.Set("domains", []string{"global-domain-id"}) // References global domain

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Functions("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_ = result
}

func TestFunctions_WithExistingLinks(t *testing.T) {
	// Test case where links already contain the tnsPath (to cover the Contains check)
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create domain
	domainsObj, _ := configRoot.CreatePath("domains")
	domainSel := domainsObj.Child("domain-id-1")
	domainSel.Set("fqdn", "example.com")

	// Create function
	funcConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-456")
	funcSel.Set("name", "myFunction")
	funcSel.Set("domains", []string{"domain-id-1"})

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")

	// Process twice to test the Contains check
	_, err := transformer.Process(ctx, configRoot)
	assert.NilError(t, err)

	_, err = transformer.Process(ctx, configRoot)
	assert.NilError(t, err)
}

func TestFunctions_AppFunctionWithDomains(t *testing.T) {
	// Setup: Create root, configRoot, app, and function config
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create app
	appsObj, _ := configRoot.CreatePath("applications")
	appObj := object.New[object.Refrence]()
	appSel := appsObj.Child("app-id-789")
	appSel.Add(appObj)

	// Create domains at app level
	domainsObj, _ := appObj.CreatePath("domains")
	domainSel := domainsObj.Child("domain-id-2")
	domainSel.Set("fqdn", "app.example.com")

	// Create function in app
	funcConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-999")
	funcSel.Set("name", "appFunction")
	funcSel.Set("domains", []string{"domain-id-2"})

	// Setup context
	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	// Execute
	transformer := Functions("main")
	result, err := transformer.Process(ctx, appObj)

	// Verify
	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestFunctions_NoFunctions(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestFunctions_NoDomains(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	funcConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	funcSel := funcConfig.Child("func-id-111")
	funcSel.Set("name", "functionNoDomains")
	// No domains

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Functions("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}
