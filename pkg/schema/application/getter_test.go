package application_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/application"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func assertApp1(t *testing.T, app application.Application) {
	assert.Equal(t, app.Get().Id(), "application1ID")
	assert.Equal(t, app.Get().Name(), "test_app1")
	assert.Equal(t, app.Get().Description(), "this is test app 1")
	assert.DeepEqual(t, app.Get().Tags(), []string{"app_tag_1", "app_tag_2"})
}

func assertApp2(t *testing.T, app application.Application) {
	assert.Equal(t, app.Get().Id(), "application2ID")
	assert.Equal(t, app.Get().Name(), "test_app2")
	assert.Equal(t, app.Get().Description(), "this is test app 2")
	assert.DeepEqual(t, app.Get().Tags(), []string{"app_tag_3", "app_tag_4"})
}

func TestGet(t *testing.T) {
	project, err := internal.NewProjectReadOnly()
	assert.NilError(t, err)

	app, err := project.Application("test_app1")
	assert.NilError(t, err)

	assertApp1(t, app)

	app, err = project.Application("test_app2")
	assert.NilError(t, err)

	assertApp2(t, app)
}
