package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDomains_WithCertificate(t *testing.T) {
	obj := object.New[object.Refrence]()
	domainsObj, _ := obj.CreatePath("domains")
	domainSel := domainsObj.Child("myDomain")
	domainSel.Set("id", "domain-id-123")
	domainSel.Set("certificate-data", "cert-data")
	domainSel.Set("certificate-key", "cert-key")
	domainSel.Set("certificate-type", "letsencrypt")

	transformer := Domains()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify domain renamed by ID
	renamedDomainSel := domainsObj.Child("domain-id-123")

	// Verify certificate attributes moved
	certFile, err := renamedDomainSel.Get("cert-file")
	assert.NilError(t, err)
	assert.Equal(t, certFile.(string), "cert-data")

	keyFile, err := renamedDomainSel.Get("key-file")
	assert.NilError(t, err)
	assert.Equal(t, keyFile.(string), "cert-key")

	certType, err := renamedDomainSel.Get("cert-type")
	assert.NilError(t, err)
	assert.Equal(t, certType.(string), "letsencrypt")

	// Verify name set
	name, err := renamedDomainSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myDomain")

	// Verify indexed
	indexPath := "domains/myDomain"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "domain-id-123")

}

func TestDomains_NoDomains(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Domains()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestDomains_MultipleDomains(t *testing.T) {
	obj := object.New[object.Refrence]()
	domainsObj, _ := obj.CreatePath("domains")

	domain1 := domainsObj.Child("domain1")
	domain1.Set("id", "id1")
	domain1.Set("certificate-data", "cert1")

	domain2 := domainsObj.Child("domain2")
	domain2.Set("id", "id2")
	domain2.Set("certificate-key", "key2")

	transformer := Domains()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both domains renamed
	_, err = domainsObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = domainsObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("domains/domain1").Exist())
	assert.Assert(t, ctx.Store().String("domains/domain2").Exist())
}
