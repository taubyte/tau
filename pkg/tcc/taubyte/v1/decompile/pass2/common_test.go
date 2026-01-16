package pass2

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestBuildIdToNameMap_RootContext(t *testing.T) {
	root := object.New[object.Refrence]()
	domains := object.New[object.Refrence]()

	domain1 := object.New[object.Refrence]()
	domain1.Set("name", "domain1")
	err := domains.Child("domain-id-1").Add(domain1)
	assert.NilError(t, err)

	domain2 := object.New[object.Refrence]()
	domain2.Set("name", "domain2")
	err = domains.Child("domain-id-2").Add(domain2)
	assert.NilError(t, err)

	err = root.Child("domains").Add(domains)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	idToName, err := buildIdToNameMap(ctx, root, "domains")
	assert.NilError(t, err)

	assert.Equal(t, idToName["domain-id-1"], "domain1")
	assert.Equal(t, idToName["domain-id-2"], "domain2")
}

func TestBuildIdToNameMap_ApplicationContext(t *testing.T) {
	// Root with global domains
	root := object.New[object.Refrence]()
	rootDomains := object.New[object.Refrence]()
	globalDomain := object.New[object.Refrence]()
	globalDomain.Set("name", "global-domain")
	err := rootDomains.Child("global-id").Add(globalDomain)
	assert.NilError(t, err)
	err = root.Child("domains").Add(rootDomains)
	assert.NilError(t, err)

	// Application with local domains
	app := object.New[object.Refrence]()
	appDomains := object.New[object.Refrence]()
	localDomain := object.New[object.Refrence]()
	localDomain.Set("name", "local-domain")
	err = appDomains.Child("local-id").Add(localDomain)
	assert.NilError(t, err)
	err = app.Child("domains").Add(appDomains)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root, app)

	// Should get both local and global domains
	idToName, err := buildIdToNameMap(ctx, app, "domains")
	assert.NilError(t, err)

	assert.Equal(t, idToName["local-id"], "local-domain")
	assert.Equal(t, idToName["global-id"], "global-domain")
}

func TestBuildIdToNameMap_LocalTakesPrecedence(t *testing.T) {
	// Root with domain
	root := object.New[object.Refrence]()
	rootDomains := object.New[object.Refrence]()
	rootDomain := object.New[object.Refrence]()
	rootDomain.Set("name", "global-name")
	err := rootDomains.Child("same-id").Add(rootDomain)
	assert.NilError(t, err)
	err = root.Child("domains").Add(rootDomains)
	assert.NilError(t, err)

	// Application with same ID but different name
	app := object.New[object.Refrence]()
	appDomains := object.New[object.Refrence]()
	appDomain := object.New[object.Refrence]()
	appDomain.Set("name", "local-name")
	err = appDomains.Child("same-id").Add(appDomain)
	assert.NilError(t, err)
	err = app.Child("domains").Add(appDomains)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root, app)

	// Local should take precedence
	idToName, err := buildIdToNameMap(ctx, app, "domains")
	assert.NilError(t, err)

	assert.Equal(t, idToName["same-id"], "local-name")
}

func TestBuildIdToNameMap_NoGroup(t *testing.T) {
	root := object.New[object.Refrence]()
	// No domains group

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	idToName, err := buildIdToNameMap(ctx, root, "domains")
	assert.NilError(t, err)
	assert.Equal(t, len(idToName), 0)
}

func TestBuildIdToNameMap_ResourcesWithoutName(t *testing.T) {
	root := object.New[object.Refrence]()
	domains := object.New[object.Refrence]()

	// Domain without name attribute
	domain1 := object.New[object.Refrence]()
	err := domains.Child("domain-id-1").Add(domain1)
	assert.NilError(t, err)

	// Domain with name
	domain2 := object.New[object.Refrence]()
	domain2.Set("name", "domain2")
	err = domains.Child("domain-id-2").Add(domain2)
	assert.NilError(t, err)

	err = root.Child("domains").Add(domains)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background(), root)

	idToName, err := buildIdToNameMap(ctx, root, "domains")
	assert.NilError(t, err)

	// Should only have domain2
	_, exists := idToName["domain-id-1"]
	assert.Assert(t, !exists, "domain without name should be skipped")
	assert.Equal(t, idToName["domain-id-2"], "domain2")
}
