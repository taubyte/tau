package services_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"github.com/taubyte/tau/pkg/schema/services"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	srv, err := project.Service("test_service1", "")
	assert.NilError(t, err)

	assertService1(t, srv.Get())

	var (
		id          = "service3ID"
		description = "this is test srv 3"
		tags        = []string{"srv_tag_5", "srv_tag_6"}
		protocol    = "/test/v1"
		smartOps    = []string{"smart1"}
	)

	err = srv.Set(true,
		services.Id(id),
		services.Description(description),
		services.Tags(tags),
		services.Protocol(protocol),
		services.SmartOps(smartOps),
	)
	assert.NilError(t, err)

	assertion := func(_srv services.Service) {
		eql(t, [][]any{
			{_srv.Get().Id(), id},
			{_srv.Get().Name(), "test_service1"},
			{_srv.Get().Description(), description},
			{_srv.Get().Tags(), tags},
			{_srv.Get().Protocol(), protocol},
			{_srv.Get().SmartOps(), smartOps},
			{_srv.Get().Application(), ""},
		})
	}
	assertion(srv)

	srv, err = project.Service("test_service1", "")
	assert.NilError(t, err)

	assertion(srv)
}

func TestSetInApp(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	srv, err := project.Service("test_service2", "test_app1")
	assert.NilError(t, err)

	assertService2(t, srv.Get())

	var (
		id          = "service3ID"
		description = "this is test srv 3"
		tags        = []string{"srv_tag_5", "srv_tag_6"}
		protocol    = "/test/v1"
	)

	err = srv.Set(true,
		services.Id(id),
		services.Description(description),
		services.Tags(tags),
		services.Protocol(protocol),
	)
	assert.NilError(t, err)

	assertion := func(_srv services.Service) {
		eql(t, [][]any{
			{_srv.Get().Id(), id},
			{_srv.Get().Name(), "test_service2"},
			{_srv.Get().Description(), description},
			{_srv.Get().Tags(), tags},
			{_srv.Get().Protocol(), protocol},
			{_srv.Get().Application(), "test_app1"},
		})
	}
	assertion(srv)

	srv, err = project.Service("test_service2", "test_app1")
	assert.NilError(t, err)

	assertion(srv)
}
