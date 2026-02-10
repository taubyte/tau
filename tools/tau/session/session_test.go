package session

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestSetGetRoundtrip(t *testing.T) {
	Clear()
	defer Clear()

	dir := t.TempDir()
	err := LoadSessionInDir(dir)
	assert.NilError(t, err)

	err = Set().ProfileName("test-profile")
	assert.NilError(t, err)
	name, ok := Get().ProfileName()
	assert.Assert(t, ok)
	assert.Equal(t, name, "test-profile")

	err = Set().SelectedProject("myproject")
	assert.NilError(t, err)
	proj, ok := Get().SelectedProject()
	assert.Assert(t, ok)
	assert.Equal(t, proj, "myproject")

	_, ok = GetSelectedCloud()
	assert.Assert(t, !ok) // not set yet
	err = Set().SelectedCloud("remote")
	assert.NilError(t, err)
	cloudType, ok := GetSelectedCloud()
	assert.Assert(t, ok)
	assert.Equal(t, cloudType, "remote")

	_, ok = GetCustomCloudUrl()
	assert.Assert(t, !ok)
	err = Set().CustomCloudUrl("sandbox.taubyte.com")
	assert.NilError(t, err)
	url, ok := GetCustomCloudUrl()
	assert.Assert(t, ok)
	assert.Equal(t, url, "sandbox.taubyte.com")
}

func TestLoadSessionInDir_EmptyLoc(t *testing.T) {
	Clear()
	defer Clear()
	err := LoadSessionInDir("")
	assert.ErrorContains(t, err, "session file location")
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	err := LoadSessionInDir(dir)
	assert.NilError(t, err)
	Clear()
	// After Clear, next Get/Set would panic or use new discovery; just ensure Clear runs
	assert.Assert(t, _session == nil)
}

func TestSessionFilePersisted(t *testing.T) {
	Clear()
	defer Clear()

	dir := t.TempDir()
	err := LoadSessionInDir(dir)
	assert.NilError(t, err)
	err = Set().ProfileName("persisted")
	assert.NilError(t, err)

	// Simulate new process: clear in-memory session and reload from dir
	_session = nil
	err = LoadSessionInDir(dir)
	assert.NilError(t, err)
	name, ok := Get().ProfileName()
	assert.Assert(t, ok)
	assert.Equal(t, name, "persisted")
}

func TestSetGet_AuthURL_DreamAPIURL(t *testing.T) {
	Clear()
	defer Clear()

	dir := t.TempDir()
	err := LoadSessionInDir(dir)
	assert.NilError(t, err)

	err = Set().AuthURL("https://auth.example.com")
	assert.NilError(t, err)
	authURL, ok := Get().AuthURL()
	assert.Assert(t, ok)
	assert.Equal(t, authURL, "https://auth.example.com")

	err = Set().DreamAPIURL("http://dream:8080")
	assert.NilError(t, err)
	dreamURL, ok := Get().DreamAPIURL()
	assert.Assert(t, ok)
	assert.Equal(t, dreamURL, "http://dream:8080")
}

func TestUnset(t *testing.T) {
	Clear()
	defer Clear()

	dir := t.TempDir()
	err := LoadSessionInDir(dir)
	assert.NilError(t, err)

	assert.NilError(t, Set().SelectedProject("p1"))
	assert.NilError(t, Unset().SelectedProject())
	proj, ok := Get().SelectedProject()
	assert.Assert(t, !ok || proj == "")

	assert.NilError(t, Set().SelectedApplication("app1"))
	assert.NilError(t, Unset().SelectedApplication())
	app, _ := Get().SelectedApplication()
	assert.Equal(t, app, "")

	assert.NilError(t, Set().AuthURL("https://a.com"))
	assert.NilError(t, Unset().AuthURL())
	_, ok = Get().AuthURL()
	assert.Assert(t, !ok)
}
