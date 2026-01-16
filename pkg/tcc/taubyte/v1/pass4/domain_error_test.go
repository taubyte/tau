package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/domain"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestDomains_PathTooShort(t *testing.T) {
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background())
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "path")
	assert.ErrorContains(t, err, "too short")
}

func TestDomains_RootNotObject(t *testing.T) {
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background(), "not-an-object", configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "root is not an object")
}

func TestDomains_FqdnNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	domainConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	domainSel := domainConfig.Child("domain-id-456")
	domainSel.Set("fqdn", 12345) // Not a string

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Domains("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "domain fqdn is not a string")
}
