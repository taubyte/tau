package generic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taubyte/tau/tools/tau/cli"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// A repository-backed kind (website, library) routes new/edit/delete through the
// shared repository driver, which registers the repo against the auth service.
// The flow is exercised in-process with the gock auth mock; attaching an
// existing repo by id+name needs no git network.
func withRepoProject(t *testing.T) (dir, projectPath, cfg string) {
	t.Helper()
	dir = t.TempDir()
	projectPath = filepath.Join(dir, "test_project")
	src, err := testutil.TCCFixtureProjectRoot()
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(projectPath, 0o755))
	assert.NilError(t, os.CopyFS(projectPath, os.DirFS(src)))
	assert.NilError(t, os.MkdirAll(filepath.Join(projectPath, "code"), 0o755))
	cfg = testutil.BasicConfigForAuthMock("test", "test_project", projectPath)
	return
}

func TestWebsiteAttachFlow(t *testing.T) {
	defer testutil.ActivateAuthMock()()
	dir, projectPath, cfg := withRepoProject(t)

	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "new", "website", "site1", "--color", "never",
		"--description", "a site",
		"--domains", "test_domain1",
		"--paths", "/",
		"--git-provider", "github",
		"--repository-id", "112233",
		"--repository-name", "taubyte-test/site1",
		"--branch", "main",
		"--no-generate-repository",
		"--no-clone",
	)
	assert.NilError(t, err)

	yaml := readFile(t, filepath.Join(projectPath, "config", "websites", "site1.yaml"))
	for _, want := range []string{"fullname: taubyte-test/site1", "id: \"112233\"", "branch: main", "/"} {
		assert.Assert(t, strings.Contains(yaml, want), "want %q in:\n%s", want, yaml)
	}

	// query renders it, list shows it
	stdout, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"query", "website", "site1", "--color", "never")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout, "taubyte-test/site1"))

	stdout, _, err = testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"query", "website", "--list", "--color", "never")
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(stdout, "site1"))

	// delete it
	_, _, err = testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "delete", "website", "site1", "--color", "never")
	assert.NilError(t, err)
	_, statErr := os.Stat(filepath.Join(projectPath, "config", "websites", "site1.yaml"))
	assert.Assert(t, os.IsNotExist(statErr))
}

func TestLibraryEditFlow(t *testing.T) {
	defer testutil.ActivateAuthMock()()
	dir, projectPath, cfg := withRepoProject(t)

	// the fixture already has test_library1; edit its branch through the repo flow
	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "edit", "library", "test_library1", "--color", "never",
		"--branch", "develop",
	)
	assert.NilError(t, err)
	yaml := readFile(t, filepath.Join(projectPath, "config", "libraries", "test_library1.yaml"))
	assert.Assert(t, strings.Contains(yaml, "branch: develop"), yaml)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	assert.NilError(t, err)
	return string(b)
}
