package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestWebsites_ResolveDomainNames(t *testing.T) {
	// Setup: Create domains and websites with domain names
	obj := object.New[object.Refrence]()

	// Create domains first (as pass1 would have done)
	domainsObj, _ := obj.CreatePath("domains")
	domain1Sel := domainsObj.Child("domain1")
	domain1Sel.Set("id", "domain-id-1")
	domain2Sel := domainsObj.Child("domain2")
	domain2Sel.Set("id", "domain-id-2")

	// Create website with domain names (not IDs)
	websitesObj, _ := obj.CreatePath("websites")
	websiteSel := websitesObj.Child("website-id-123")
	websiteSel.Set("domains", []string{"domain1", "domain2"})

	// Index domains (as pass1 would have done)
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	err := utils.IndexById(ctx, "domains", "domain1", "domain-id-1")
	assert.NilError(t, err)
	err = utils.IndexById(ctx, "domains", "domain2", "domain-id-2")
	assert.NilError(t, err)

	// Execute: Run pass2 transformer
	transformer := Websites()
	_, err = transformer.Process(ctx, obj)

	// Verify: Domain names resolved to IDs
	assert.NilError(t, err)

	resolvedDomains, err := websitesObj.Child("website-id-123").Get("domains")
	assert.NilError(t, err)
	domainIds := resolvedDomains.([]string)
	assert.Equal(t, len(domainIds), 2)
	assert.Equal(t, domainIds[0], "domain-id-1")
	assert.Equal(t, domainIds[1], "domain-id-2")

}

func TestWebsites_NoWebsites(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Websites()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestWebsites_NoDomains(t *testing.T) {
	obj := object.New[object.Refrence]()

	websitesObj, _ := obj.CreatePath("websites")
	websiteSel := websitesObj.Child("website-id-456")
	websiteSel.Set("github-fullname", "repo/name")
	// No domains field

	transformer := Websites()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestWebsites_MultipleWebsites(t *testing.T) {
	obj := object.New[object.Refrence]()

	// Setup domains
	domainsObj, _ := obj.CreatePath("domains")
	domainSel := domainsObj.Child("mydomain")
	domainSel.Set("id", "domain-id-999")

	// Setup websites
	websitesObj, _ := obj.CreatePath("websites")
	website1Sel := websitesObj.Child("website-id-1")
	website1Sel.Set("domains", []string{"mydomain"})
	website2Sel := websitesObj.Child("website-id-2")
	website2Sel.Set("domains", []string{"mydomain"})

	// Index domain
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	utils.IndexById(ctx, "domains", "mydomain", "domain-id-999")

	// Execute
	transformer := Websites()
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both resolved
	domains1, _ := websitesObj.Child("website-id-1").Get("domains")
	assert.Equal(t, domains1.([]string)[0], "domain-id-999")

	domains2, _ := websitesObj.Child("website-id-2").Get("domains")
	assert.Equal(t, domains2.([]string)[0], "domain-id-999")
}
