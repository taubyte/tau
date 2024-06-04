package application_test

import (
	"testing"

	"github.com/taubyte/tau/pkg/schema/application"
	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestSetBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	app, err := project.Application("test_app1")
	assert.NilError(t, err)

	assertApp1(t, app)

	var (
		id          = "application3ID"
		description = "this is test app 3"
		tags        = []string{"app_tag_5", "app_tag_6"}
	)

	err = app.Set(true,
		application.Id(id),
		application.Description(description),
		application.Tags(tags),
	)
	assert.NilError(t, err)

	assert.Equal(t, app.Get().Id(), id)
	assert.Equal(t, app.Get().Description(), description)
	assert.DeepEqual(t, app.Get().Tags(), tags)

	app, err = project.Application("test_app1")
	assert.NilError(t, err)

	assert.Equal(t, app.Get().Id(), id)
	assert.Equal(t, app.Get().Description(), description)
	assert.DeepEqual(t, app.Get().Tags(), tags)
}
