package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestLibraries_WithGitHubSource(t *testing.T) {
	obj := object.New[object.Refrence]()
	librariesObj, _ := obj.CreatePath("libraries")
	libSel := librariesObj.Child("myLibrary")
	libSel.Set("id", "lib-id-123")
	libSel.Set("github-fullname", "taubyte/library")
	libSel.Set("github-id", "789012")
	libSel.Set("git-provider", "github")

	transformer := Libraries()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify library renamed by ID
	renamedLibSel := librariesObj.Child("lib-id-123")

	// Verify attributes moved
	repoName, err := renamedLibSel.Get("repository-name")
	assert.NilError(t, err)
	assert.Equal(t, repoName.(string), "taubyte/library")

	repoId, err := renamedLibSel.Get("repository-id")
	assert.NilError(t, err)
	assert.Equal(t, repoId.(string), "789012")

	provider, err := renamedLibSel.Get("provider")
	assert.NilError(t, err)
	assert.Equal(t, provider.(string), "github")

	// Verify name set
	name, err := renamedLibSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myLibrary")

	// Verify indexed
	indexPath := "libraries/myLibrary"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "lib-id-123")

}

func TestLibraries_NoLibraries(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Libraries()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestLibraries_MultipleLibraries(t *testing.T) {
	obj := object.New[object.Refrence]()
	librariesObj, _ := obj.CreatePath("libraries")

	lib1 := librariesObj.Child("library1")
	lib1.Set("id", "id1")
	lib1.Set("github-fullname", "repo1")
	lib1.Set("github-id", "111")
	lib1.Set("git-provider", "github")

	lib2 := librariesObj.Child("library2")
	lib2.Set("id", "id2")
	lib2.Set("github-fullname", "repo2")
	lib2.Set("github-id", "222")
	lib2.Set("git-provider", "github")

	transformer := Libraries()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both libraries renamed
	_, err = librariesObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = librariesObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("libraries/library1").Exist())
	assert.Assert(t, ctx.Store().String("libraries/library2").Exist())
}
