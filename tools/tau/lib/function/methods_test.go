package functionLib

import (
	"sort"
	"testing"

	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestList_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	names, err := List()
	assert.NilError(t, err)
	// TCC fixture global: test_function1_glob, test_function2_glob
	assert.Equal(t, len(names), 2)
	sort.Strings(names)
	assert.DeepEqual(t, names, []string{"test_function1_glob", "test_function2_glob"})
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
	assert.DeepEqual(t, names, []string{"test_function1_glob", "test_function2_glob"})
}

func TestProjectFunctionCount_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	proj, err := projectLib.SelectedProjectInterface()
	assert.NilError(t, err)
	n := ProjectFunctionCount(proj)
	// Global: 2. test_app1: 2 (test_function2, test_function4). test_app2: 2 (test_function2, test_function3).
	assert.Equal(t, n, 6)
}

func TestList_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	names, err := List()
	assert.NilError(t, err)
	assert.Equal(t, len(names), 2)
	sort.Strings(names)
	assert.DeepEqual(t, names, []string{"test_function2", "test_function4"})
}

func TestListResources_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	resources, err := ListResources()
	assert.NilError(t, err)
	assert.Equal(t, len(resources), 2)
	names := make([]string, len(resources))
	for i, r := range resources {
		names[i] = r.Name
	}
	sort.Strings(names)
	assert.DeepEqual(t, names, []string{"test_function2", "test_function4"})
}
