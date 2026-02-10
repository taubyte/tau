package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestGetSelectedProject_FromSession(t *testing.T) {
	config.Clear()
	session.Clear()
	defer func() {
		config.Clear()
		session.Clear()
	}()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	assert.NilError(t, os.MkdirAll(sessionPath, 0755))
	assert.NilError(t, session.LoadSessionInDir(sessionPath))
	restore := testutil.WithConfigPath(configPath)
	defer restore()

	config.Projects()
	assert.NilError(t, config.Projects().Set("proj1", config.Project{Location: "/tmp/proj1"}))
	assert.NilError(t, session.Set().SelectedProject("proj1"))

	name, err := config.GetSelectedProject()
	assert.NilError(t, err)
	assert.Equal(t, name, "proj1")
}

func TestGetSelectedProject_NotFound(t *testing.T) {
	config.Clear()
	session.Clear()
	defer func() {
		config.Clear()
		session.Clear()
	}()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	assert.NilError(t, os.MkdirAll(sessionPath, 0755))
	assert.NilError(t, session.LoadSessionInDir(sessionPath))
	restore := testutil.WithConfigPath(configPath)
	defer restore()

	config.Projects()
	// No project set in session, no project in config list

	_, err := config.GetSelectedProject()
	assert.Assert(t, err != nil)
}

func TestGetSelectedUser_FromSession(t *testing.T) {
	config.Clear()
	session.Clear()
	defer func() {
		config.Clear()
		session.Clear()
	}()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	assert.NilError(t, os.MkdirAll(sessionPath, 0755))
	assert.NilError(t, session.LoadSessionInDir(sessionPath))
	restore := testutil.WithConfigPath(configPath)
	defer restore()

	config.Profiles()
	config.Projects()
	assert.NilError(t, session.Set().ProfileName("myprofile"))

	name, err := config.GetSelectedUser()
	assert.NilError(t, err)
	assert.Equal(t, name, "myprofile")
}

func TestGetSelectedApplication_Empty(t *testing.T) {
	session.Clear()
	defer session.Clear()

	dir := t.TempDir()
	assert.NilError(t, session.LoadSessionInDir(dir))
	app, ok := config.GetSelectedApplication()
	assert.Assert(t, !ok)
	assert.Equal(t, app, "")
}

func TestGetSelectedUser_FromProjectDefaultProfile(t *testing.T) {
	config.Clear()
	session.Clear()
	defer func() {
		config.Clear()
		session.Clear()
	}()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	assert.NilError(t, os.MkdirAll(sessionPath, 0755))
	assert.NilError(t, session.LoadSessionInDir(sessionPath))
	restore := testutil.WithConfigPath(configPath)
	defer restore()

	config.Profiles()
	assert.NilError(t, config.Profiles().Set("defaultprofile", config.Profile{Default: true}))
	config.Projects()
	assert.NilError(t, config.Projects().Set("proj1", config.Project{Location: "/tmp/p1", DefaultProfile: "defaultprofile"}))
	assert.NilError(t, session.Set().SelectedProject("proj1"))

	name, err := config.GetSelectedUser()
	assert.NilError(t, err)
	assert.Equal(t, name, "defaultprofile")
}

func TestGetSelectedUser_NotFound(t *testing.T) {
	config.Clear()
	session.Clear()
	defer func() {
		config.Clear()
		session.Clear()
	}()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")
	assert.NilError(t, os.MkdirAll(sessionPath, 0755))
	assert.NilError(t, session.LoadSessionInDir(sessionPath))
	restore := testutil.WithConfigPath(configPath)
	defer restore()

	config.Profiles()
	config.Projects()

	_, err := config.GetSelectedUser()
	assert.Assert(t, err != nil)
}

func TestProjects_ListAndDelete(t *testing.T) {
	config.Clear()
	defer config.Clear()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")
	restore := testutil.WithConfigPath(configPath)
	defer restore()

	projects := config.Projects()
	list := projects.List()
	assert.Equal(t, len(list), 0)

	assert.NilError(t, projects.Set("p1", config.Project{Location: "/tmp/p1"}))
	list = projects.List()
	assert.Equal(t, len(list), 1)
	assert.Equal(t, list["p1"].Location, "/tmp/p1")

	assert.NilError(t, projects.Delete("p1"))
	list = projects.List()
	assert.Equal(t, len(list), 0)
}
