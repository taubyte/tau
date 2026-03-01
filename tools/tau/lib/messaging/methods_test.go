package messagingLib

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestList_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	names, err := List()
	assert.NilError(t, err)
	// TCC fixture global: test_messaging1
	assert.Equal(t, len(names), 1)
	assert.Equal(t, names[0], "test_messaging1")
}

func TestListResources_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	resources, err := ListResources()
	assert.NilError(t, err)
	assert.Equal(t, len(resources), 1)
	assert.Equal(t, resources[0].Name, "test_messaging1")
}

func TestList_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	names, err := List()
	assert.NilError(t, err)
	assert.Equal(t, len(names), 1)
	assert.Equal(t, names[0], "test_messaging2")
}

func TestListResources_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	resources, err := ListResources()
	assert.NilError(t, err)
	assert.Equal(t, len(resources), 1)
	assert.Equal(t, resources[0].Name, "test_messaging2")
}
