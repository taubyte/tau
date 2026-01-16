package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/website"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestWebsites_PathTooShort(t *testing.T) {
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background())
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "path")
	assert.ErrorContains(t, err, "too short")
}

func TestWebsites_ProjectIdNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", 12345) // Not a string

	websiteConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-456")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "project id is not a string")
}

func TestWebsites_ProviderNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	websiteConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-456")
	websiteSel.Set("provider", 12345) // Not a string
	websiteSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "git provider is not a string")
}

func TestWebsites_RepositoryIdNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	websiteConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	websiteSel := websiteConfig.Child("website-id-456")
	websiteSel.Set("provider", "github")
	websiteSel.Set("repository-id", 12345) // Not a string

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Websites("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "git repository is not a string")
}
