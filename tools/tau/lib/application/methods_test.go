package applicationLib

import (
	"sort"
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestList_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	names, err := List()
	assert.NilError(t, err)
	// TCC fixture has test_app1, test_app2
	assert.Equal(t, len(names), 2)
	sort.Strings(names)
	assert.DeepEqual(t, names, []string{"test_app1", "test_app2"})
}

func TestGet_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	app, err := Get("test_app1")
	assert.NilError(t, err)
	assert.Equal(t, app.Name, "test_app1")
	assert.Equal(t, app.Description, "this is test app 1")
	assert.DeepEqual(t, app.Tags, []string{"app_tag_1", "app_tag_2"})
}

func TestListResources_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	resources, err := ListResources()
	assert.NilError(t, err)
	assert.Equal(t, len(resources), 2)
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.Name
	}
	sort.Strings(names)
	assert.DeepEqual(t, names, []string{"test_app1", "test_app2"})
}

func TestSelectedProjectAndApp_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	proj, app, err := SelectedProjectAndApp()
	assert.NilError(t, err)
	assert.Assert(t, proj != nil)
	assert.Equal(t, app, "")
}

func TestSelectedProjectAndApp_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	proj, app, err := SelectedProjectAndApp()
	assert.NilError(t, err)
	assert.Assert(t, proj != nil)
	assert.Equal(t, app, "test_app1")
}

func TestSelect_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	err := Select(nil, "test_app1")
	assert.NilError(t, err)
	app, ok := session.Get().SelectedApplication()
	assert.Assert(t, ok)
	assert.Equal(t, app, "test_app1")
}

func TestDeselect_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	err := Deselect(nil, "")
	assert.NilError(t, err)
	app, _ := session.Get().SelectedApplication()
	assert.Equal(t, app, "")
}
