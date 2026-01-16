package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestWebsites_GlobalWebsiteWithDomains(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create domains
	domainsObj, _ := configRoot.CreatePath("domains")
	domainSel := domainsObj.Child("domain-id-1")
	domainSel.Set("fqdn", "example.com")

	// Create website
	websiteConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-456")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", "123456")
	websiteSel.Set("domains", []string{"domain-id-1"})

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)
	assert.Assert(t, indexes != nil)

}

func TestWebsites_AppWebsiteWithGlobalDomainFallback(t *testing.T) {
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

	// Create website in app with domain reference
	websiteConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-999")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", "999888")
	websiteSel.Set("domains", []string{"global-domain-id"})

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Websites("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_ = result
}

func TestWebsites_AppWebsiteWithSecondaryDomainFallback(t *testing.T) {
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

	// Create website in app referencing global domain (not app domain)
	websiteConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-999")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", "999888")
	websiteSel.Set("domains", []string{"global-domain-id"}) // References global domain

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Websites("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_ = result
}

func TestWebsites_AppWebsiteWithDomains(t *testing.T) {
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

	// Create website in app
	websiteConfig, _ := appObj.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-999")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", "789012")
	websiteSel.Set("domains", []string{"domain-id-2"})

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(appObj)

	transformer := Websites("main")
	result, err := transformer.Process(ctx, appObj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestWebsites_NoWebsites(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestWebsites_NoDomains(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	websiteConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-111")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", "111222")
	// No domains

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}
