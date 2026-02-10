package login_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

const loginFlowConfig = `
profiles: {}
projects:
  test_project:
    defaultprofile: ""
    location: test_project
`

func TestLoginFlow_CreateNewProfile(t *testing.T) {
	loginLib.ExtractInfoStub = func(_, _ string) (string, string, error) {
		return "testUser", "test@test.com", nil
	}
	defer func() { loginLib.ExtractInfoStub = nil }()

	dir := t.TempDir()
	stdout, _, err := testutil.RunCLIWithDir(t, cli.Run, dir, loginFlowConfig,
		"login", "someProfile",
		"-p", "github", "-t", "123456",
		"--color", "never",
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout, fmt.Sprintf("Created default profile: %s", "someProfile")))

	name, ok := session.Get().ProfileName()
	assert.Assert(t, ok, "profile name should be set")
	assert.Equal(t, name, "someProfile")
}

func TestLoginFlow_CreateDefaultProfile(t *testing.T) {
	loginLib.ExtractInfoStub = func(_, _ string) (string, string, error) {
		return "testUser", "test@test.com", nil
	}
	defer func() { loginLib.ExtractInfoStub = nil }()

	dir := t.TempDir()
	_, _, err := testutil.RunCLIWithDir(t, cli.Run, dir, loginFlowConfig,
		"login", "someProfile",
		"-p", "github", "-t", "123456", "--color", "never",
	)
	assert.NilError(t, err)

	stdout, _, err := testutil.RunCLIWithDir(t, cli.Run, dir, "", // don't overwrite config so someProfile is kept
		"login", "someProfile2",
		"-p", "github", "-t", "123456",
		"--new", "--set-default", "--color", "never",
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout, fmt.Sprintf("Created default profile: %s", "someProfile2")))

	name, ok := session.Get().ProfileName()
	assert.Assert(t, ok, "profile name should be set")
	assert.Equal(t, name, "someProfile2")
}

func TestLoginFlow_SelectProfile(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, "test_project")
	assert.NilError(t, os.MkdirAll(projectPath, 0755))
	// Config with two profiles so we can select one without relying on CLI persistence
	twoProfilesConfig := `
profiles:
  someProfile:
    provider: github
    token: "123456"
    default: true
    network: sandbox.taubyte.com
  someProfile2:
    provider: github
    token: "123456"
    default: false
    network: sandbox.taubyte.com
projects:
  test_project:
    defaultprofile: someProfile
    location: ` + projectPath + "\n"

	stdout, _, err := testutil.RunCLIWithDir(t, cli.Run, dir, twoProfilesConfig,
		"login", "someProfile", "--color", "never",
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout, fmt.Sprintf("Selected profile: %s", "someProfile")))

	name, ok := session.Get().ProfileName()
	assert.Assert(t, ok, "profile name should be set")
	assert.Equal(t, name, "someProfile")
}
