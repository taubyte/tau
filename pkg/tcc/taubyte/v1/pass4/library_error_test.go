package pass4

import (
	"context"
	"testing"

	specs "github.com/taubyte/tau/pkg/specs/library"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestLibraries_PathTooShort(t *testing.T) {
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	ctx := transform.NewContext[object.Refrence](context.Background())
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "path")
	assert.ErrorContains(t, err, "too short")
}

func TestLibraries_ProjectIdNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", 12345) // Not a string

	libraryConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	libSel := libraryConfig.Child("lib-id-456")
	libSel.Set("name", "myLibrary")
	libSel.Set("provider", "github")
	libSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "project id is not a string")
}

func TestLibraries_ProviderNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	libraryConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	libSel := libraryConfig.Child("lib-id-456")
	libSel.Set("name", "myLibrary")
	libSel.Set("provider", 12345) // Not a string
	libSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "git provider is not a string")
}

func TestLibraries_NameNotString(t *testing.T) {
	root := object.New[object.Refrence]()
	configRoot := object.New[object.Refrence]()
	configRoot.Set("id", "project-id-123")

	libraryConfig, _ := configRoot.CreatePath(string(specs.PathVariable))
	libSel := libraryConfig.Child("lib-id-456")
	libSel.Set("name", 12345) // Not a string
	libSel.Set("provider", "github")
	libSel.Set("repository-id", "123456")

	ctx := transform.NewContext[object.Refrence](context.Background(), root, configRoot)
	ctx = ctx.Fork(configRoot)

	transformer := Libraries("main")
	_, err := transformer.Process(ctx, configRoot)

	assert.ErrorContains(t, err, "library name is not a string")
}
