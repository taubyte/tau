package application_test

import (
	"testing"

	internal "github.com/taubyte/tau/pkg/schema/internal/test"
	"gotest.tools/v3/assert"
)

func TestDeleteBasic(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	app, err := project.Application("test_app2")
	assert.NilError(t, err)

	assertApp2(t, app)

	err = app.Delete()
	assert.NilError(t, err)
	internal.AssertEmpty(t,
		app.Get().Id(),
		app.Get().Name(),
		app.Get().Description(),
		app.Get().Tags(),
	)

	assert.Equal(t, len(project.Get().Applications()), 1)

	app, err = project.Application("test_app2")
	assert.NilError(t, err)

	assert.Equal(t, app.Get().Name(), "test_app2")
	internal.AssertEmpty(t,
		app.Get().Id(),
		app.Get().Description(),
		app.Get().Tags(),
	)
}

func TestDeleteAttributes(t *testing.T) {
	project, close, err := internal.NewProjectCopy()
	assert.NilError(t, err)
	defer close()

	app, err := project.Application("test_app1")
	assert.NilError(t, err)

	assertApp1(t, app)

	err = app.Delete("description", "tags")
	assert.NilError(t, err)

	assert.Equal(t, app.Get().Id(), "application1ID")
	assert.Equal(t, app.Get().Name(), "test_app1")
	internal.AssertEmpty(t, app.Get().Description(), app.Get().Tags())

	// Re-open
	app, err = project.Application("test_app1")
	assert.NilError(t, err)

	assert.Equal(t, app.Get().Id(), "application1ID")
	assert.Equal(t, app.Get().Name(), "test_app1")
	internal.AssertEmpty(t, app.Get().Description(), app.Get().Tags())
}
