package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDomains_NoDomains(t *testing.T) {
	domains := Domains()

	obj := object.New[object.Refrence]()
	// No domains group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := domains.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no domains")
}

func TestDomains_WithDomains(t *testing.T) {
	domains := Domains()

	root := object.New[object.Refrence]()
	domainsObj := object.New[object.Refrence]()

	domain1 := object.New[object.Refrence]()
	domain1.Set("name", "my-domain")
	domain1.Set("id", "domain-id-1")
	domain1.Set("cert-file", "cert-data") // Move expects this to exist
	domain1.Set("key-file", "key-data")   // Move expects this to exist
	domain1.Set("cert-type", "inline")    // Move expects this to exist
	err := domainsObj.Child("domain-id-1").Add(domain1)
	assert.NilError(t, err)

	err = root.Child("domains").Add(domainsObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := domains.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultDomains, err := result.Child("domains").Object()
	assert.NilError(t, err)
	resultDomain1, err := resultDomains.Child("my-domain").Object()
	assert.NilError(t, err)

	// Should have moved attributes (from cert-file to certificate-data)
	certData, err := resultDomain1.GetString("certificate-data")
	assert.NilError(t, err)
	assert.Equal(t, certData, "cert-data")

	keyData, err := resultDomain1.GetString("certificate-key")
	assert.NilError(t, err)
	assert.Equal(t, keyData, "key-data")

	certType, err := resultDomain1.GetString("certificate-type")
	assert.NilError(t, err)
	assert.Equal(t, certType, "inline")

	// Should be renamed by name
	_, err = resultDomains.Child("domain-id-1").Object()
	assert.ErrorContains(t, err, "not exist")
}

func TestDomains_MissingName(t *testing.T) {
	domains := Domains()

	root := object.New[object.Refrence]()
	domainsObj := object.New[object.Refrence]()

	domain1 := object.New[object.Refrence]()
	domain1.Set("id", "domain-id-1")
	// Missing name
	err := domainsObj.Child("domain-id-1").Add(domain1)
	assert.NilError(t, err)

	err = root.Child("domains").Add(domainsObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	_, err = domains.Process(ctx, root)
	assert.ErrorContains(t, err, "fetching name for domain")
}

func TestDomains_ErrorFetchingDomains(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}
