package pass3

import (
	"context"
	"testing"

	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
	"gotest.tools/v3/assert"
)

func TestApplications_NoApplications(t *testing.T) {
	applications := Applications()

	obj := object.New[object.Refrence]()
	// No applications group

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := applications.Process(ctx, obj)
	assert.NilError(t, err)
	assert.Assert(t, result == obj, "should return same object when no applications")
}

func TestApplications_WithApplications(t *testing.T) {
	applications := Applications()

	root := object.New[object.Refrence]()
	apps := object.New[object.Refrence]()

	app1 := object.New[object.Refrence]()
	app1.Set("name", "my-app")
	app1.Set("id", "app-id-1")
	err := apps.Child("app-id-1").Add(app1)
	assert.NilError(t, err)

	app2 := object.New[object.Refrence]()
	app2.Set("name", "another-app")
	app2.Set("id", "app-id-2")
	err = apps.Child("app-id-2").Add(app2)
	assert.NilError(t, err)

	err = root.Child("applications").Add(apps)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	result, err := applications.Process(ctx, root)
	assert.NilError(t, err)

	// Check that applications were renamed by name
	resultApps, err := result.Child("applications").Object()
	assert.NilError(t, err)

	// Should be keyed by name now
	app1Result, err := resultApps.Child("my-app").Object()
	assert.NilError(t, err)
	id, err := app1Result.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "app-id-1")

	app2Result, err := resultApps.Child("another-app").Object()
	assert.NilError(t, err)
	id, err = app2Result.GetString("id")
	assert.NilError(t, err)
	assert.Equal(t, id, "app-id-2")
}

func TestApplications_MissingName(t *testing.T) {
	applications := Applications()

	root := object.New[object.Refrence]()
	apps := object.New[object.Refrence]()

	app1 := object.New[object.Refrence]()
	app1.Set("id", "app-id-1")
	// Missing name
	err := apps.Child("app-id-1").Add(app1)
	assert.NilError(t, err)

	err = root.Child("applications").Add(apps)
	assert.NilError(t, err)

	ctx := transform.NewContext[object.Refrence](context.Background())
	_, err = applications.Process(ctx, root)
	assert.ErrorContains(t, err, "fetching name for application")
}

func TestApplications_ErrorFetchingApplications(t *testing.T) {
	// Setting a string value doesn't create a child object, so Child().Object() will return ErrNotExist
	// This test case is not realistic - skip it
	t.Skip("Skipping - setting string value doesn't create child object")
}
