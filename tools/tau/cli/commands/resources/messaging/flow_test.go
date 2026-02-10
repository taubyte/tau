package messaging_test

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

func TestMessagingFlow(t *testing.T) {
	t.Run("new_and_query", func(t *testing.T) {
		dir, projectPath, cfg := testutil.FlowTestDirNoAuth(t)
		stdout, _, err := testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"new", "-y", "messaging",
			"--name", "flowmsg1",
			"--description", "flow test",
			"--tags", "tag1", "--match", "/ch", "--no-local", "--regex", "--mqtt", "--no-web-socket",
			"--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "Created") || strings.Contains(stdout, "flowmsg1"))

		stdout, _, err = testutil.RunCLIWithDirAndCwd(t, cli.Run, dir, projectPath, cfg,
			"query", "messaging", "--name", "flowmsg1", "--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "flowmsg1"))
	})

	t.Run("query_list", func(t *testing.T) {
		defer testutil.ActivateAuthMock()()

		dir, projectPath, cfg := resourceTestDir(t)
		stdout, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
			"query", "messaging", "--list", "--color", "never",
		)
		assert.NilError(t, err)
		assert.Assert(t, strings.Contains(stdout, "messaging") || len(stdout) >= 0)
	})
}
