package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	"gotest.tools/v3/assert"
)

func TestVerifyWorkDirExists_NotExist(t *testing.T) {
	err := verifyWorkDirExists(filepath.Join(t.TempDir(), "nonexistent"))
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "not cloned"), "error: %v", err)
}

func TestVerifyWorkDirExists_FileNotDir(t *testing.T) {
	f, err := os.CreateTemp("", "tau-build-test-*")
	assert.NilError(t, err)
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	err = verifyWorkDirExists(path)
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "not a directory"))
}

func TestVerifyWorkDirExists_Exists(t *testing.T) {
	dir := t.TempDir()
	err := verifyWorkDirExists(dir)
	assert.NilError(t, err)
}

func TestBuildContext_WorkDirForFunction_NoApp(t *testing.T) {
	bc := &buildContext{
		projectConfig: config.Project{Location: "/project"},
		selectedApp:   "",
	}
	wd := bc.workDirForFunction("myfunc")
	assert.Assert(t, wd == "/project/code/functions/myfunc" || (strings.Contains(wd, "functions") && strings.Contains(wd, "myfunc")))
}

func TestBuildContext_WorkDirForFunction_WithApp(t *testing.T) {
	bc := &buildContext{
		projectConfig: config.Project{Location: "/project"},
		selectedApp:   "myapp",
	}
	wd := bc.workDirForFunction("myfunc")
	assert.Assert(t, strings.Contains(wd, "myapp") && strings.Contains(wd, "myfunc"))
}

func TestBuildContext_WorkDirForWebsite(t *testing.T) {
	bc := &buildContext{
		projectConfig: config.Project{Location: "/project"},
	}
	wd, err := bc.workDirForWebsite("user/repo")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(wd, "repo"))
	assert.Assert(t, strings.Contains(wd, "website") || strings.Contains(wd, "websites"))
}

func TestBuildContext_WorkDirForWebsite_Invalid(t *testing.T) {
	bc := &buildContext{projectConfig: config.Project{Location: "/project"}}
	_, err := bc.workDirForWebsite("invalid")
	assert.Assert(t, err != nil)
	assert.Equal(t, err, errInvalidRepoName)
}

func TestBuildContext_WorkDirForLibrary(t *testing.T) {
	bc := &buildContext{
		projectConfig: config.Project{Location: "/project"},
	}
	wd, err := bc.workDirForLibrary("user/librepo")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(wd, "librepo"))
}

func TestBuildContext_WorkDirForLibrary_Invalid(t *testing.T) {
	bc := &buildContext{projectConfig: config.Project{Location: "/project"}}
	_, err := bc.workDirForLibrary("n slash")
	assert.Assert(t, err != nil)
}
