package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// FlowTestDirNoAuth creates a temp dir with test_project/config and test_project/code, and returns
// (dir, projectPath, configYAML) for flow tests that do not use the auth mock. Use with
// testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, configYAML, ...).
func FlowTestDirNoAuth(t *testing.T) (dir, projectPath, configYAML string) {
	t.Helper()
	dir = t.TempDir()
	projectPath = filepath.Join(dir, "test_project")
	if err := os.MkdirAll(filepath.Join(projectPath, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, "code"), 0755); err != nil {
		t.Fatal(err)
	}
	configYAML = `profiles:
  test:
    provider: github
    token: 123456
    default: true
    git_username: taubyte-test
    git_email: taubytetest@gmail.com
    type: Remote
    network: sandbox.taubyte.com
projects:
  test_project:
    defaultprofile: test
    location: ` + projectPath + "\n"
	return dir, projectPath, configYAML
}
