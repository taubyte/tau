package storages_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/common"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Storage("test_storage1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "storage1ID"},
		{_struct.Name, "test_storage1"},
		{_struct.Description, "a streaming storage"},
		{_struct.Tags, []string{"storage_tag_1", "storage_tag_2"}},
		{_struct.Match, "photos"},
		{_struct.Regex, true},
		{_struct.Public, false},
		{_struct.Versioning, false},
		{common.TimeToString(_struct.Ttl), "5m"},
		{common.UnitsToString(_struct.Size), "30GB"},
		{_struct.Type, "streaming"},
		{len(_struct.SmartOps), 0},
	})

	db, err = project.Storage("test_storage2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "storage2ID"},
		{_struct.Name, "test_storage2"},
		{_struct.Description, "an object storage"},
		{_struct.Tags, []string{"storage_tag_3", "storage_tag_4"}},
		{_struct.Match, "users"},
		{_struct.Regex, false},
		{_struct.Public, true},
		{_struct.Versioning, true},
		{common.TimeToString(_struct.Ttl), "0s"},
		{common.UnitsToString(_struct.Size), "50GB"},
		{_struct.Type, "object"},
		{len(_struct.SmartOps), 0},
	})
}
