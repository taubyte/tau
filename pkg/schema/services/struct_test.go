package services_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"gotest.tools/v3/assert"
)

func TestStruct(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	srv, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	err = srv.SetWithStruct(true, &structureSpec.Service{
		Id:          "service1ID",
		Description: "a super simple protocol",
		Tags:        []string{"service_tag_1", "service_tag_2"},
		Protocol:    "/simple/v1",
		SmartOps:    []string{},
	})
	assert.NilError(t, err)

	assertService1(t, srv.Get())

	srv, err = project.Service("test_service1", "")
	assert.NilError(t, err)

	assertService1(t, srv.Get())
}

func TestStructError(t *testing.T) {
	project, err := internal.NewProjectEmpty()
	assert.NilError(t, err)

	srv, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	err = srv.SetWithStruct(true, nil)
	assert.ErrorContains(t, err, "nil pointer")
}
