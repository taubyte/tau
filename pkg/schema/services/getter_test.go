package services_test

import (
	"fmt"
	"runtime"
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/services"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func eql(t *testing.T, a [][]any) {
	_, file, line, _ := runtime.Caller(2)
	for idx, pair := range a {
		switch pair[0].(type) {
		case []string:
			comp := cmp.DeepEqual(pair[0], pair[1])
			assert.Check(t, comp, fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		default:
			assert.Equal(t, pair[0], pair[1], fmt.Sprintf("item(%d): %s:%d", idx, file, line))
		}
	}
}

func assertService1(t *testing.T, getter services.Getter) {
	eql(t, [][]any{
		{getter.Id(), "service1ID"},
		{getter.Name(), "test_service1"},
		{getter.Description(), "a super simple protocol"},
		{getter.Tags(), []string{"service_tag_1", "service_tag_2"}},
		{getter.Protocol(), "/simple/v1"},
		{getter.Application(), ""},
		{len(getter.SmartOps()), 0},
	})
}

func assertService2(t *testing.T, getter services.Getter) {
	eql(t, [][]any{
		{getter.Id(), "service2ID"},
		{getter.Name(), "test_service2"},
		{getter.Description(), "a simple protocol"},
		{getter.Tags(), []string{"service_tag_3", "service_tag_4"}},
		{getter.Protocol(), "/simple/v2"},
		{getter.Application(), "test_app1"},
		{len(getter.SmartOps()), 0},
	})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	srv, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	assertService1(t, srv.Get())

	srv, err = project.Service("test_service2", "test_app1")
	assert.NilError(t, err)

	assertService2(t, srv.Get())
}
