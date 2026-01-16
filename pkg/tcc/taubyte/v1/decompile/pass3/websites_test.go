package pass3

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

func TestWebsites_WithWebsites(t *testing.T) {
	websites := Websites()

	root := object.New[object.Refrence]()
	websitesObj := object.New[object.Refrence]()

	website1 := object.New[object.Refrence]()
	website1.Set("name", "my-website")
	website1.Set("id", "website-id-1")
	website1.Set("repository-name", "owner/repo")
	website1.Set("repository-id", "12345")
	website1.Set("provider", "github")
	err := websitesObj.Child("website-id-1").Add(website1)
	assert.NilError(t, err)

	err = root.Child("websites").Add(websitesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := websites.Process(ctx, root)
	assert.NilError(t, err)

	// Check transformations
	resultWebsites, err := result.Child("websites").Object()
	assert.NilError(t, err)
	resultWebsite1, err := resultWebsites.Child("my-website").Object()
	assert.NilError(t, err)

	// Should have moved attributes (from repository-name to github-fullname, etc.)
	fullname, err := resultWebsite1.GetString("github-fullname")
	assert.NilError(t, err)
	assert.Equal(t, fullname, "owner/repo")

	githubId, err := resultWebsite1.GetString("github-id")
	assert.NilError(t, err)
	assert.Equal(t, githubId, "12345")

	gitProvider, err := resultWebsite1.GetString("git-provider")
	assert.NilError(t, err)
	assert.Equal(t, gitProvider, "github")
}

func TestWebsites_MissingName(t *testing.T) {
	websites := Websites()

	root := object.New[object.Refrence]()
	websitesObj := object.New[object.Refrence]()

	website1 := object.New[object.Refrence]()
	website1.Set("id", "website-id-1")
	// Missing name
	err := websitesObj.Child("website-id-1").Add(website1)
	assert.NilError(t, err)

	err = root.Child("websites").Add(websitesObj)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	_, err = websites.Process(ctx, root)
	assert.ErrorContains(t, err, "fetching name for website")
}
