package databases_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Database("test_database1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "database1ID"},
		{_struct.Name, "test_database1"},
		{_struct.Description, "a database for users"},
		{_struct.Tags, []string{"database_tag_1", "database_tag_2"}},
		{_struct.Match, "/users"},
		{_struct.Regex, true},
		{_struct.Local, false},
		{_struct.Min, 15},
		{_struct.Max, 30},
		{len(_struct.SmartOps), 0},
	})

	db, err = project.Database("test_database2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "database2ID"},
		{_struct.Name, "test_database2"},
		{_struct.Description, "a profiles database"},
		{_struct.Tags, []string{"database_tag_3", "database_tag_4"}},
		{_struct.Match, "profiles"},
		{_struct.Regex, false},
		{_struct.Local, true},
		{_struct.Min, 42},
		{_struct.Max, 601},
		{len(_struct.SmartOps), 0},
	})
}
