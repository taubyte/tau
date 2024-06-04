package website_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStructBasic(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	err = web.SetWithStruct(true, &structureSpec.Website{
		Id:          "website1ID",
		Description: "a simple photo booth",
		Tags:        []string{"website_tag_1", "website_tag_2"},
		Domains:     []string{"test_domain1"},
		Paths:       []string{"/photos"},
		Branch:      "main",
		Provider:    "github",
		RepoID:      "111111111",
		RepoName:    "taubyte-test/photo_booth",
	})
	assert.NilError(t, err)

	assertWebsite1(t, web.Get())

	web, err = project.Website("test_website2", "test_app1")
	assert.NilError(t, err)

	// Use different cert type
	err = web.SetWithStruct(true, &structureSpec.Website{
		Id:          "website2ID",
		Description: "my portfolio",
		Tags:        []string{"website_tag_3", "website_tag_4"},
		Domains:     []string{"test_domain2"},
		Paths:       []string{"/portfolio"},
		Branch:      "main",
		Provider:    "github",
		RepoID:      "222222222",
		RepoName:    "taubyte-test/portfolio",
	})
	assert.NilError(t, err)

	assertWebsite2(t, web.Get())
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	web, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	err = web.SetWithStruct(true, &structureSpec.Website{
		Id:       "website1ID",
		SmartOps: []string{"smart1"},
	})
	assert.NilError(t, err)

	eql(t, [][]any{
		{web.Get().Id(), "website1ID"},
		{web.Get().SmartOps(), []string{"smart1"}},
	})

	err = web.SetWithStruct(true, &structureSpec.Website{
		Provider: "unsupported",
	})
	assert.ErrorContains(t, err, "Git provider `unsupported` not supported")
}
