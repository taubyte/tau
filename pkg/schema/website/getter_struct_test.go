package website_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Website("test_website1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "website1ID"},
		{_struct.Name, "test_website1"},
		{_struct.Description, "a simple photo booth"},
		{_struct.Tags, []string{"website_tag_1", "website_tag_2"}},
		{_struct.Domains, []string{"test_domain1"}},
		{_struct.Paths, []string{"/photos"}},
		{_struct.Branch, "main"},
		{_struct.Provider, "github"},
		{_struct.RepoID, "111111111"},
		{_struct.RepoName, "taubyte-test/photo_booth"},
		{len(_struct.SmartOps), 0},
	})

	db, err = project.Website("test_website2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "website2ID"},
		{_struct.Name, "test_website2"},
		{_struct.Description, "my portfolio"},
		{_struct.Tags, []string{"website_tag_3", "website_tag_4"}},
		{_struct.Domains, []string{"test_domain2"}},
		{_struct.Paths, []string{"/portfolio"}},
		{_struct.Branch, "main"},
		{_struct.Provider, "github"},
		{_struct.RepoID, "222222222"},
		{_struct.RepoName, "taubyte-test/portfolio"},
		{len(_struct.SmartOps), 0},
	})
}
