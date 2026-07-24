package session_test

import (
	"testing"

	"github.com/taubyte/tau/tools/tau/session"
	"gotest.tools/v3/assert"
)

// The session store is the single source of the CLI's selections; set/get/unset
// round-trip for each selection without touching a real session file beyond the
// temp dir.
func TestSelectionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	session.Clear()
	assert.NilError(t, session.LoadSessionInDir(dir))
	t.Cleanup(session.Clear)

	set := session.Set()
	assert.NilError(t, set.ProfileName("p1"))
	assert.NilError(t, set.SelectedProject("proj"))
	assert.NilError(t, set.SelectedApplication("app"))
	assert.NilError(t, set.SelectedCloud("cloud"))
	assert.NilError(t, set.CustomCloudUrl("http://x"))

	get := session.Get()
	assertGet(t, get.ProfileName, "p1")
	assertGet(t, get.SelectedProject, "proj")
	assertGet(t, get.SelectedApplication, "app")
	assertGet(t, get.SelectedCloud, "cloud")
	assertGet(t, get.CustomCloudUrl, "http://x")

	// the package-level convenience getters agree
	c, ok := session.GetSelectedCloud()
	assert.Assert(t, ok)
	assert.Equal(t, c, "cloud")
	u, ok := session.GetCustomCloudUrl()
	assert.Assert(t, ok)
	assert.Equal(t, u, "http://x")

	unset := session.Unset()
	assert.NilError(t, unset.ProfileName())
	assert.NilError(t, unset.SelectedProject())
	assert.NilError(t, unset.SelectedApplication())
	assert.NilError(t, unset.SelectedCloud())
	assert.NilError(t, unset.CustomCloudUrl())

	_, ok = session.Get().SelectedProject()
	assert.Assert(t, !ok)
}

func assertGet(t *testing.T, fn func() (string, bool), want string) {
	t.Helper()
	got, ok := fn()
	assert.Assert(t, ok)
	assert.Equal(t, got, want)
}
