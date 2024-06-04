package smartops_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/common"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.SmartOps("test_smartops1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "smartops1ID"},
		{_struct.Name, "test_smartops1"},
		{_struct.Description, "verifies node has GPU"},
		{_struct.Tags, []string{"smart_tag_1", "smart_tag_2"}},
		{_struct.Source, "."},
		{common.TimeToString(_struct.Timeout), "6m40s"},
		{common.UnitsToString(_struct.Memory), "16MB"},
		{_struct.Call, "ping1"},
	})

	db, err = project.SmartOps("test_smartops2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "smartops2ID"},
		{_struct.Name, "test_smartops2"},
		{_struct.Description, "verifies user is on a specific continent"},
		{_struct.Tags, []string{"smart_tag_3", "smart_tag_4"}},
		{_struct.Source, "library/test_library2"},
		{common.TimeToString(_struct.Timeout), "5m"},
		{common.UnitsToString(_struct.Memory), "64MB"},
		{_struct.Call, "ping2"},
	})
}
