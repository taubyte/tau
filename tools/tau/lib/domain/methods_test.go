package domainLib

import (
	"strings"
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
	// TCC fixture has at least global test_domain1
	assert.Assert(t, len(names) >= 1)
	assert.Assert(t, strings.Contains(strings.Join(names, ","), "test_domain1"))
}

func TestListResources_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	resources, err := ListResources()
	assert.NilError(t, err)
	assert.Assert(t, len(resources) >= 1)
	var found bool
	for _, r := range resources {
		if r.Name == "test_domain1" {
			found = true
			assert.Equal(t, r.Fqdn, "hal.computers.com")
			break
		}
	}
	assert.Assert(t, found)
}

func TestProjectDomainCount_TCCFixture(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	proj, err := projectLib.SelectedProjectInterface()
	assert.NilError(t, err)
	n := ProjectDomainCount(proj)
	// At least global + test_app1 (test_domain2)
	assert.Assert(t, n >= 2)
}

func TestList_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	names, err := List()
	assert.NilError(t, err)
	assert.Equal(t, len(names), 1)
	assert.Equal(t, names[0], "test_domain2")
}

func TestListResources_TCCFixture_WithApp(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedApplication("test_app1")
	resources, err := ListResources()
	assert.NilError(t, err)
	assert.Equal(t, len(resources), 1)
	assert.Equal(t, resources[0].Name, "test_domain2")
}

func TestIsAGeneratedFQDN_TestCloud(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedCloud("test")
	ok, err := IsAGeneratedFQDN("prefix-abc12345.g.tau.link")
	assert.NilError(t, err)
	assert.Assert(t, ok)
	ok, err = IsAGeneratedFQDN("random.com")
	assert.NilError(t, err)
	assert.Assert(t, !ok)
}

func TestNewGeneratedFQDN_TestCloud(t *testing.T) {
	testutil.WithTCCFixtureEnv(t)
	session.Set().SelectedCloud("test")
	fqdn, err := NewGeneratedFQDN("myapp")
	assert.NilError(t, err)
	assert.Assert(t, len(fqdn) > 0)
	assert.Assert(t, strings.HasPrefix(fqdn, "myapp-"))
	assert.Assert(t, strings.HasSuffix(fqdn, ".g.tau.link"))
}
