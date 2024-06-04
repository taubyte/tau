package services_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/services"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	srv, err := project.Service("test_service2", "test_app1")
	assert.NilError(t, err)

	assertService2(t, srv.Get())

	err = srv.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		srv.Get().Id(),
		srv.Get().Name(),
		srv.Get().Description(),
		srv.Get().Tags(),
		srv.Get().Protocol(),
	)

	local, _ := project.Get().Services("test_app1")
	assert.Equal(t, len(local), 0)

	srv, err = project.Service("test_service2", "test_app1")
	assert.NilError(t, err)

	assert.Equal(t, srv.Get().Name(), "test_service2")
	internal.AssertEmpty(t,
		srv.Get().Id(),
		srv.Get().Description(),
		srv.Get().Tags(),
		srv.Get().Protocol(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	srv, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	assertService1(t, srv.Get())

	err = srv.Delete("description", "protocol")
	assert.NilError(t, err)

	assertion := func(_srv services.Service) {
		eql(t, [][]any{
			{_srv.Get().Id(), "service1ID"},
			{_srv.Get().Name(), "test_service1"},
			{_srv.Get().Description(), ""},
			{_srv.Get().Tags(), []string{"service_tag_1", "service_tag_2"}},
			{_srv.Get().Protocol(), ""},
			{_srv.Get().Application(), ""},
		})
	}
	assertion(srv)

	// Re-open
	srv, err = project.Service("test_service1", "")
	assert.NilError(t, err)

	assert.Equal(t, srv.Get().Id(), "service1ID")
	assert.Equal(t, srv.Get().Name(), "test_service1")
	assertion(srv)
}
