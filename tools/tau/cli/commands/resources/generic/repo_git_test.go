package generic_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/cli"
	repositoryLib "github.com/taubyte/tau/tools/tau/lib/repository"
	"github.com/taubyte/tau/tools/tau/testutil"
	"gotest.tools/v3/assert"
)

// fakeGit is a test double for the CLI's git surface — no remote, no disk repo.
type fakeGit struct {
	root                 string
	pushed, pulled       bool
	committed, checkedTo string
}

func (f *fakeGit) Commit(message, files string) error { f.committed = message; return nil }
func (f *fakeGit) Push() error                        { f.pushed = true; return nil }
func (f *fakeGit) Pull() error                        { f.pulled = true; return nil }
func (f *fakeGit) Checkout(branch string) error       { f.checkedTo = branch; return nil }
func (f *fakeGit) Root() string                       { return f.root }
func (f *fakeGit) ListBranches(bool) ([]string, error, error) {
	return []string{"main", "dev"}, nil, nil
}

// withFakeGit swaps the git seam for the flow's duration. NewRepository creates
// the working dir so a chained clone→checkout (and later push/pull/checkout)
// sees the repo as present.
func withFakeGit(t *testing.T, repoDir string) *fakeGit {
	t.Helper()
	fake := &fakeGit{root: repoDir}
	orig := repositoryLib.NewRepository
	repositoryLib.NewRepository = func(context.Context, ...git.Option) (repositoryLib.GitRepository, error) {
		_ = os.MkdirAll(repoDir, 0o755)
		return fake, nil
	}
	t.Cleanup(func() { repositoryLib.NewRepository = orig })
	return fake
}

// The fixture's test_website1 points at taubyte-test/photo_booth; its clone dir
// is <project>/websites/photo_booth.
func websiteRepoDir(projectPath string) string {
	return filepath.Join(projectPath, "websites", "photo_booth")
}

func TestWebsiteCloneFlow(t *testing.T) {
	defer testutil.ActivateAuthMock()()
	dir, projectPath, cfg := withRepoProject(t)
	fake := withFakeGit(t, websiteRepoDir(projectPath))

	// clone runs clone then checkout; the branch comes from ListBranches (main).
	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "clone", "website", "--name", "test_website1", "--color", "never")
	assert.NilError(t, err)
	assert.Equal(t, fake.checkedTo, "main")
}

func TestWebsitePushPullCheckout(t *testing.T) {
	defer testutil.ActivateAuthMock()()
	dir, projectPath, cfg := withRepoProject(t)
	repoDir := websiteRepoDir(projectPath)
	fake := withFakeGit(t, repoDir)
	assert.NilError(t, os.MkdirAll(repoDir, 0o755)) // already cloned

	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "push", "website", "--name", "test_website1", "--message", "wip", "--color", "never")
	assert.NilError(t, err)
	assert.Assert(t, fake.pushed)

	_, _, err = testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "pull", "website", "--name", "test_website1", "--color", "never")
	assert.NilError(t, err)
	assert.Assert(t, fake.pulled)

	_, _, err = testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "checkout", "website", "--name", "test_website1", "--branch", "dev", "--color", "never")
	assert.NilError(t, err)
	assert.Equal(t, fake.checkedTo, "dev")
}

// new --clone attaches an existing repo and clones it in one shot.
func TestWebsiteNewWithClone(t *testing.T) {
	defer testutil.ActivateAuthMock()()
	dir, projectPath, cfg := withRepoProject(t)
	fake := withFakeGit(t, filepath.Join(projectPath, "websites", "fresh"))

	_, _, err := testutil.RunCLIWithDirAndCwdWithAuthMock(t, cli.Run, dir, projectPath, cfg,
		"--defaults", "-y", "new", "website", "wsite", "--color", "never",
		"--git-provider", "github",
		"--repository-id", "998877",
		"--repository-name", "taubyte-test/fresh",
		"--branch", "main",
		"--no-generate-repository",
		"--clone",
	)
	assert.NilError(t, err)
	yaml := readFile(t, filepath.Join(projectPath, "config", "websites", "wsite.yaml"))
	assert.Assert(t, len(yaml) > 0)
	_ = fake
}
