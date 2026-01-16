package pass1

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestWebsites_WithGitHubSource(t *testing.T) {
	obj := object.New[object.Refrence]()
	websitesObj, _ := obj.CreatePath("websites")
	websiteSel := websitesObj.Child("myWebsite")
	websiteSel.Set("id", "website-id-123")
	websiteSel.Set("github-fullname", "taubyte/example")
	websiteSel.Set("github-id", "123456")
	websiteSel.Set("git-provider", "github")

	transformer := Websites()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify website renamed by ID
	renamedWebsiteSel := websitesObj.Child("website-id-123")

	// Verify attributes moved
	repoName, err := renamedWebsiteSel.Get("repository-name")
	assert.NilError(t, err)
	assert.Equal(t, repoName.(string), "taubyte/example")

	repoId, err := renamedWebsiteSel.Get("repository-id")
	assert.NilError(t, err)
	assert.Equal(t, repoId.(string), "123456")

	provider, err := renamedWebsiteSel.Get("provider")
	assert.NilError(t, err)
	assert.Equal(t, provider.(string), "github")

	// Verify name set
	name, err := renamedWebsiteSel.Get("name")
	assert.NilError(t, err)
	assert.Equal(t, name.(string), "myWebsite")

	// Verify indexed
	indexPath := "websites/myWebsite"
	assert.Assert(t, ctx.Store().String(indexPath).Exist())
	assert.Equal(t, ctx.Store().String(indexPath).Get(), "website-id-123")

}

func TestWebsites_NoWebsites(t *testing.T) {
	obj := object.New[object.Refrence]()

	transformer := Websites()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	result, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)
	assert.Assert(t, result != nil)
}

func TestWebsites_MultipleWebsites(t *testing.T) {
	obj := object.New[object.Refrence]()
	websitesObj, _ := obj.CreatePath("websites")

	website1 := websitesObj.Child("website1")
	website1.Set("id", "id1")
	website1.Set("github-fullname", "repo1")
	website1.Set("github-id", "111")
	website1.Set("git-provider", "github")

	website2 := websitesObj.Child("website2")
	website2.Set("id", "id2")
	website2.Set("github-fullname", "repo2")
	website2.Set("github-id", "222")
	website2.Set("git-provider", "github")

	transformer := Websites()
	ctx := transform.NewContext[object.Refrence](context.Background(), obj)
	_, err := transformer.Process(ctx, obj)

	assert.NilError(t, err)

	// Verify both websites renamed
	_, err = websitesObj.Child("id1").Object()
	assert.NilError(t, err)

	_, err = websitesObj.Child("id2").Object()
	assert.NilError(t, err)

	// Verify both indexed
	assert.Assert(t, ctx.Store().String("websites/website1").Exist())
	assert.Assert(t, ctx.Store().String("websites/website2").Exist())
}
