package services_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestGetStruct(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	db, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	_struct, err := db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "service1ID"},
		{_struct.Name, "test_service1"},
		{_struct.Description, "a super simple protocol"},
		{_struct.Tags, []string{"service_tag_1", "service_tag_2"}},
		{_struct.Protocol, "/simple/v1"},
		{len(_struct.SmartOps), 0},
	})

	db, err = project.Service("test_service2", "test_app1")
	assert.NilError(t, err)

	_struct, err = db.Get().Struct()
	assert.NilError(t, err)

	eql(t, [][]any{
		{_struct.Id, "service2ID"},
		{_struct.Name, "test_service2"},
		{_struct.Description, "a simple protocol"},
		{_struct.Tags, []string{"service_tag_3", "service_tag_4"}},
		{_struct.Protocol, "/simple/v2"},
		{len(_struct.SmartOps), 0},
	})
}
