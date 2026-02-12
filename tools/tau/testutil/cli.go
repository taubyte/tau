package testutil

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/constants"
	"github.com/taubyte/tau/tools/tau/session"
	"github.com/urfave/cli/v2"
)

// RunCommand builds a minimal cli.App with the given command as the only
// subcommand and runs it with args. The first element of args is the
// program name (e.g. "tau"); the rest are command name, flags, and positionals.
// UseShortOptionHandling is enabled to match the real tau app.
// Example: RunCommand(login.Command, "tau", "login", "profileName", "--provider", "github", "--token", "token")
func RunCommand(cmd *cli.Command, args ...string) error {
	app := &cli.App{
		Name:                   "tau",
		UseShortOptionHandling: true,
		Commands:               []*cli.Command{cmd},
	}
	return app.Run(args)
}

// AppRunner runs the tau CLI with the given args. Pass cli.Run from flow tests.
type AppRunner func(args ...string) error

// RunCLI runs the full tau CLI with the given runner, config YAML and args. It uses a temp dir
// for config and session, captures stdout, and returns (stdout, stderr, err). Clear and
// restore singletons so tests stay isolated. Use for flow tests: testutil.RunCLI(t, cli.Run, configYAML, args...).
func RunCLI(t *testing.T, runApp AppRunner, configYAML string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return RunCLIWithDir(t, runApp, t.TempDir(), configYAML, args...)
}

// RunCLIWithDir runs the full tau CLI with config and session under dir. Use the same dir
// for multiple commands to share session state (e.g. login flow: create profile then select).
// If configYAML is empty, the config file is not written (use existing config in dir).
// It runs with current working directory set to dir.
func RunCLIWithDir(t *testing.T, runApp AppRunner, dir, configYAML string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCLIWithDirAndCwd(t, runApp, dir, dir, configYAML, false, args...)
}

// RunCLIWithDirAndCwd is like RunCLIWithDir but runs with working directory cwd (e.g. a project
// subdir so GetSelectedProject finds the project). If cwd is empty, dir is used.
func RunCLIWithDirAndCwd(t *testing.T, runApp AppRunner, dir, cwd, configYAML string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	if cwd == "" {
		cwd = dir
	}
	return runCLIWithDirAndCwd(t, runApp, dir, cwd, configYAML, false, args...)
}

// RunCLIWithDirAndCwdWithAuthMock is like RunCLIWithDirAndCwd but sets TAUBYTE_AUTH_URL and session SelectedCloud("test")
// for the auth mock (test cloud). Use for flow tests that use ActivateAuthMock (gock).
func RunCLIWithDirAndCwdWithAuthMock(t *testing.T, runApp AppRunner, dir, cwd, configYAML string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	if cwd == "" {
		cwd = dir
	}
	return runCLIWithDirAndCwd(t, runApp, dir, cwd, configYAML, true, args...)
}

func runCLIWithDirAndCwd(t *testing.T, runApp AppRunner, dir, cwd, configYAML string, withAuthMock bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	configPath := filepath.Join(dir, "tau.yaml")
	sessionPath := filepath.Join(dir, "session")

	if configYAML != "" {
		if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
			return "", "", err
		}
	}
	if err := os.MkdirAll(sessionPath, 0755); err != nil {
		return "", "", err
	}

	session.Clear()
	config.Clear()
	t.Cleanup(func() {
		session.Clear()
		config.Clear()
	})

	if err := session.LoadSessionInDir(sessionPath); err != nil {
		return "", "", err
	}
	if withAuthMock {
		oldAuthURL := os.Getenv("TAUBYTE_AUTH_URL")
		os.Setenv("TAUBYTE_AUTH_URL", AuthMockBaseURL)
		t.Cleanup(func() {
			if oldAuthURL == "" {
				os.Unsetenv("TAUBYTE_AUTH_URL")
			} else {
				os.Setenv("TAUBYTE_AUTH_URL", oldAuthURL)
			}
		})
		session.Set().SelectedCloud("test")
	}

	t.Cleanup(WithConfigPath(configPath))

	oldTauConfig := constants.TauConfigFileName
	constants.TauConfigFileName = configPath
	t.Cleanup(func() { constants.TauConfigFileName = oldTauConfig })

	oldDir, dirErr := os.Getwd()
	if dirErr != nil {
		return "", "", dirErr
	}
	if err := os.Chdir(cwd); err != nil {
		return "", "", err
	}
	t.Cleanup(func() { os.Chdir(oldDir) })

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	pterm.SetDefaultOutput(w)
	t.Cleanup(func() {
		os.Stdout = oldStdout
		pterm.SetDefaultOutput(oldStdout)
	})

	fullArgs := append([]string{"prog"}, args...)
	runErr := runApp(fullArgs...)
	w.Close()
	out, _ := io.ReadAll(r)
	stdout = string(out)
	if runErr != nil {
		stderr = runErr.Error()
		return stdout, stderr, runErr
	}
	return stdout, "", nil
}
