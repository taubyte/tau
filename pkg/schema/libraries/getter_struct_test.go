package libraries_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Library("test_library1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "library1ID"},
		{_struct.Name, "test_library1"},
		{_struct.Description, "just a library"},
		{_struct.Tags, []string{"library_tag_1", "library_tag_2"}},
		{_struct.Path, "/"},
		{_struct.Branch, "main"},
		{_struct.Provider, "github"},
		{_struct.RepoID, "111111111"},
		{_struct.RepoName, "taubyte-test/library1"},
		{len(_struct.SmartOps), 0},
	})

	db, err = project.Library("test_library2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "library2ID"},
		{_struct.Name, "test_library2"},
		{_struct.Description, "just another library"},
		{_struct.Tags, []string{"library_tag_3", "library_tag_4"}},
		{_struct.Path, "/src"},
		{_struct.Branch, "dreamland"},
		{_struct.Provider, "github"},
		{_struct.RepoID, "222222222"},
		{_struct.RepoName, "taubyte-test/library2"},
		{len(_struct.SmartOps), 0},
	})
}
