package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/domain"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDomains_GlobalDomain(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create domain
	domainConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	domainSel := domainConfig.Child("domain-id-456")
	domainSel.Set("fqdn", "example.com")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	// Verify indexes created
	indexes, err := root.Child("indexes").Object()
	assert.NilError(t, err)
	assert.Assert(t, indexes != nil)

}

func TestDomains_MultipleDomains(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create multiple domains
	domainConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	domain1Sel := domainConfig.Child("domain-id-1")
	domain1Sel.Set("fqdn", "example1.com")
	domain2Sel := domainConfig.Child("domain-id-2")
	domain2Sel.Set("fqdn", "example2.com")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

}

func TestDomains_NoDomains(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestDomains_WithExistingIndex(t *testing.T) {
	// Test case where index already exists (to cover the nil check path)
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	// Create domain
	domainConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	domainSel := domainConfig.Child("domain-id-456")
	domainSel.Set("fqdn", "example.com")

	// Pre-create index with a value (not nil)
	indexes, _ := root.CreatePath("indexes")
	// Create a path that would be used
	indexes.Set("some/path", []string{"existing"})

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	result, err := transformer.Process(ctx, configRoot)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)

	_ = result
}
