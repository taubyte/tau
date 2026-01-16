package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestLibraries_NoLibraries(t *testing.T) {
	libraries := Libraries()

	obj := object.New[object.Refrence]()
	// No libraries group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := libraries.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no libraries")
}

func TestLibraries_WithLibraries(t *testing.T) {
	libraries := Libraries()

	root := object.New[object.Refrence]()
	librariesObj := object.New[object.Refrence]()

	lib1 := object.New[object.Refrence]()
	lib1.Set("name", "my-library")
	lib1.Set("id", "lib-id-1")
	lib1.Set("repository-name", "owner/repo") // Move expects this to exist
	lib1.Set("repository-id", "12345")        // Move expects this to exist
	lib1.Set("provider", "github")            // Move expects this to exist
	err := librariesObj.Child("lib-id-1").Add(lib1)
	assert.NilError(t, err)

	err = root.Child("libraries").Add(librariesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := libraries.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultLibraries, err := result.Child("libraries").Object()
	assert.NilError(t, err)
	resultLib1, err := resultLibraries.Child("my-library").Object()
	assert.NilError(t, err)

	// Should have moved attributes (from repository-name to github-fullname, etc.)
	fullname, err := resultLib1.GetString("github-fullname")
	assert.NilError(t, err)
	assert.Equal(t, fullname, "owner/repo")

	githubId, err := resultLib1.GetString("github-id")
	assert.NilError(t, err)
	assert.Equal(t, githubId, "12345")

	gitProvider, err := resultLib1.GetString("git-provider")
	assert.NilError(t, err)
	assert.Equal(t, gitProvider, "github")

	// Should be renamed by name
	_, err = resultLibraries.Child("lib-id-1").Object()
	assert.ErrorContains(t, err, "not exist")
}

func TestLibraries_MissingName(t *testing.T) {
	libraries := Libraries()

	root := object.New[object.Refrence]()
	librariesObj := object.New[object.Refrence]()

	lib1 := object.New[object.Refrence]()
	lib1.Set("id", "lib-id-1")
	// Missing name
	err := librariesObj.Child("lib-id-1").Add(lib1)
	assert.NilError(t, err)

	err = root.Child("libraries").Add(librariesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	_, err = libraries.Process(ctx, root)
	assert.ErrorContains(t, err, "fetching name for library")
}

func TestLibraries_ErrorFetchingLibraries(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}
