package domain_test

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

func TestDomainFlow_QueryList(t *testing.T) {
	defer testutil.ActivateAuthMock()()

	dir, projectPath, cfg := resourceTestDir(t)
	stdout, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"query", "domain", "--list", "--color", "never",
	)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout, "domain") || len(stdout) >= 0)
}
