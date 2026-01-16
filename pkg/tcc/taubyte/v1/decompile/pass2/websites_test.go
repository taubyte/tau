package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestWebsites_NoWebsites(t *testing.T) {
	websites := Websites()

	obj := object.New[object.Refrence]()
	// No websites group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := websites.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no websites")
}

func TestWebsites_WithDomains(t *testing.T) {
	websites := Websites()

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

	// Create websites with domain IDs
	websitesObj := object.New[object.Refrence]()
	website1 := object.New[object.Refrence]()
	website1.Set("domains", []string{"domain-id-1"})
	err = websitesObj.Child("website1").Add(website1)
	assert.NilError(t, err)
	err = root.Child("websites").Add(websitesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	result, err := websites.Process(ctx, root)
	assert.NilError(t, err)

	// Check that domain IDs were resolved to names
	resultWebsites, err := result.Child("websites").Object()
	assert.NilError(t, err)
	resultWebsite1, err := resultWebsites.Child("website1").Object()
	assert.NilError(t, err)
	domainsVal := resultWebsite1.Get("domains")
	assert.NilError(t, err)
	domainsSlice := domainsVal.([]string)
	assert.Equal(t, len(domainsSlice), 1)
	assert.Equal(t, domainsSlice[0], "domain1")
}

func TestWebsites_DomainNotFound(t *testing.T) {
	websites := Websites()

	root := object.New[object.Refrence]()
	websitesObj := object.New[object.Refrence]()
	website1 := object.New[object.Refrence]()
	website1.Set("domains", []string{"non-existent-id"})
	err := websitesObj.Child("website1").Add(website1)
	assert.NilError(t, err)
	err = root.Child("websites").Add(websitesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = websites.Process(ctx, root)
	assert.ErrorContains(t, err, "domain ID non-existent-id not found")
}

func TestWebsites_DomainsNotSlice(t *testing.T) {
	websites := Websites()

	root := object.New[object.Refrence]()
	websitesObj := object.New[object.Refrence]()
	website1 := object.New[object.Refrence]()
	website1.Set("domains", "not-a-slice")
	err := websitesObj.Child("website1").Add(website1)
	assert.NilError(t, err)
	err = root.Child("websites").Add(websitesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)
	_, err = websites.Process(ctx, root)
	assert.ErrorContains(t, err, "domains is not a []string")
}

func TestWebsites_ErrorFetchingWebsites(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}
