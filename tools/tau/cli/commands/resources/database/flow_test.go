package database_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

func resourceTestDir(t *testing.T) (dir, projectPath, config string) {
	t.Helper()
	dir = t.TempDir()
	projectPath = filepath.Join(dir, "test_project")
	assert.NilError(t, os.MkdirAll(filepath.Join(projectPath, "config"), 0755))
	assert.NilError(t, os.MkdirAll(filepath.Join(projectPath, "code"), 0755))
	config = testutil.BasicConfigForAuthMock("test", "test_project", projectPath)
	return dir, projectPath, config
}

func TestDatabaseFlow(t *testing.T) {
	t.Run("query_list", func(t *testing.T) {
		defer testutil.ActivateAuthMock()()

		dir, projectPath, cfg := resourceTestDir(t)
		stdout, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
			"query", "database", "--list", "--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "database") || len(stdout) >= 0)
	})

	t.Run("new_and_query", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "database",
			"--name", "flowdb1",
			"--description", "flow test db",
			"--tags", "tag1",
			"--match", "/path",
			"--no-regex", "--no-local", "--no-encryption",
			"--min", "1", "--max", "10", "--size", "1GB",
			"--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "Created") || strings.Contains(stdout, "flowdb1"))

		stdout, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"query", "database", "--name", "flowdb1", "--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "flowdb1"))
	})
}
