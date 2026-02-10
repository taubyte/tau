package login

import (
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/tools/tau/config"
	loginLib "github.com/taubyte/tau/tools/tau/lib/login"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func TestLogin_NewProfile_NonInteractive(t *testing.T) {
	// Avoid real GitHub API call so test does not hang on network.
	loginLib.ExtractInfoStub = func(_, _ string) (string, string, error) {
		return "testUser", "test@test.com", nil
	}
	defer func() { loginLib.ExtractInfoStub = nil }()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")

	session.Clear()
	config.Clear()
	t.Cleanup(func() {
		session.Clear()
		config.Clear()
	})

	t.Cleanup(testutil.WithConfigPath(configPath))

	// Flags before positional to ensure they are parsed by the command
	err := testutil.RunCommand(Command, "tau", "login",
		"--provider", "github",
		"--token", "123456",
		"testProfile",
	)
	assert.NilError(t, err)

	// Command loads session during run; assert selected profile
	name, ok := session.Get().ProfileName()
	assert.Assert(t, ok, "profile name should be set")
	assert.Equal(t, name, "testProfile")
}

func TestLogin_SelectProfile_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "tau.yaml")

	session.Clear()
	config.Clear()
	t.Cleanup(func() {
		session.Clear()
		config.Clear()
	})

	t.Cleanup(testutil.WithConfigPath(configPath))

	// Avoid real GitHub API so tests do not hang.
	loginLib.ExtractInfoStub = func(_, _ string) (string, string, error) {
		return "testUser", "test@test.com", nil
	}
	defer func() { loginLib.ExtractInfoStub = nil }()

	// Create two profiles first (flags before positional)
	err := testutil.RunCommand(Command, "tau", "login",
		"--provider", "github", "--token", "123456", "first")
	assert.NilError(t, err)

	err = testutil.RunCommand(Command, "tau", "login",
		"--new", "--set-default", "--provider", "github", "--token", "123456", "second")
	assert.NilError(t, err)

	// Select first profile (non-interactive: name as first arg)
	err = testutil.RunCommand(Command, "tau", "login", "first")
	assert.NilError(t, err)

	name, ok := session.Get().ProfileName()
	assert.Assert(t, ok, "profile name should be set")
	assert.Equal(t, name, "first")
}

func TestLogin_Help(t *testing.T) {
	err := testutil.RunCommand(Command, "tau", "login", "--help")
	assert.NilError(t, err)
}
