package git

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"gotest.tools/v3/assert"
)

var (
	testRepoGitUrl  = "git@github.com:taubyte-test/for-tests.git"
	testRepoHTTPUrl = "https://github.com/taubyte-test/for-tests.git"
	testRepoUser    = "taubyte-test"
	testRepoName    = "for-tests"
	testRepoEmail   = "taubytetest@gmail.com"
)

func testRepoToken(t *testing.T) (tkn string) {
	if tkn = os.Getenv("TEST_GIT_TOKEN"); tkn == "" {
		t.SkipNow()
	}
	return
}

func TestNew(t *testing.T) {
	_, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root(t.TempDir()),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)
}

func TestTempWithRoot(t *testing.T) {
	testRoot := "repo"

	repo, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root(testRoot),
		Temporary(),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

	if len(repo.Root()) == 0 {
		t.Errorf("repo.Root() got nothing")
		return
	}

	_, err = os.Stat(repo.workDir)
	assert.NilError(t, err)

	if path.Join(repo.workDir, testRoot) != repo.root {
		t.Errorf("Wrong workdir got `%s` expected `%s`", path.Join(repo.workDir, testRoot), repo.root)
		return
	}
}

func TestTempWithNoRoot(t *testing.T) {
	repo, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Temporary(),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

	if len(repo.Root()) == 0 {
		t.Errorf("repo.Root() got nothing")
		return
	}

	_, err = os.Stat(repo.root)
	assert.NilError(t, err)

	if repo.root != repo.workDir {
		t.Errorf("No root provided workdir `%s`should be the same as repo root `%s`", repo.workDir, repo.root)
		return
	}
}

func TestCommit(t *testing.T) {
	root := t.TempDir()
	c, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root(root),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

	err = os.WriteFile(root+"/plain.txt", []byte(fmt.Sprint("hello world", time.Now())), 0755)
	assert.NilError(t, err)

	err = c.Commit("Adding plain file", "plain.txt")
	assert.NilError(t, err)
}

func TestPush(t *testing.T) {
	root := t.TempDir()

	c, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root(root),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

	assert.NilError(t, os.WriteFile(root+"/timestamp.txt", []byte(time.Now().String()), 0640))

	assert.NilError(t, c.Commit(t.Name(), "."))

	assert.NilError(t, c.Push())
}

func TestCloneFail(t *testing.T) {
	dir := t.TempDir()

	_, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token("wrongauth"),
		Root(dir),
		Author(testRepoUser, testRepoEmail),
	)
	assert.Error(t, err, "authentication required")

	assert.NilError(t, os.RemoveAll(dir))
}

func TestCloneWithDeployKey(t *testing.T) {
	pubKey, secKey, err := generateDeployKey()
	assert.NilError(t, err)

	ctx, ctxC := context.WithTimeout(context.Background(), 10*time.Second)
	defer ctxC()

	githubClient := githubApiClient(ctx, testRepoToken(t))

	err = injectDeploymentKey(ctx, githubClient, testRepoUser, testRepoName, "go-simple-git-clone-with-deploy-key", pubKey)
	if err != nil {
		t.Error(err)
	}

	_, err = New(
		context.Background(),
		URL(testRepoGitUrl),
		SSHKey(secKey),
		Root(t.TempDir()),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

}

func TestConvertSSHToHTTPS(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "GitHub SSH URL",
			input:    "git@github.com:user/repo.git",
			expected: "https://github.com/user/repo.git",
		},
		{
			name:     "GitHub SSH URL without .git suffix",
			input:    "git@github.com:user/repo",
			expected: "https://github.com/user/repo.git",
		},
		{
			name:     "GitLab SSH URL",
			input:    "git@gitlab.com:user/repo.git",
			expected: "https://gitlab.com/user/repo.git",
		},
		{
			name:     "HTTPS URL (should remain unchanged)",
			input:    "https://github.com/user/repo.git",
			expected: "https://github.com/user/repo.git",
		},
		{
			name:     "Non-SSH URL (should remain unchanged)",
			input:    "http://github.com/user/repo.git",
			expected: "http://github.com/user/repo.git",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertSSHToHTTPS(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCheckoutEmptyRepo asserts that Checkout on an empty repo (no commits) only sets HEAD
// and does not create symrefs to origin or call worktree checkout.
func TestCheckoutEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	r, err := gogit.PlainInit(dir, false)
	assert.NilError(t, err)

	_, err = r.CreateRemote(&config.RemoteConfig{
		Name:  gogit.DefaultRemoteName,
		URLs:  []string{"https://example.com/repo.git"},
		Fetch: []config.RefSpec{config.RefSpec(fmt.Sprintf(config.DefaultFetchRefSpec, gogit.DefaultRemoteName))},
	})
	assert.NilError(t, err)

	mainRef := plumbing.ReferenceName("refs/heads/main")
	assert.NilError(t, r.CreateBranch(&config.Branch{Name: "main", Remote: gogit.DefaultRemoteName, Merge: mainRef}))
	headRef := plumbing.NewSymbolicReference(plumbing.HEAD, mainRef)
	assert.NilError(t, r.Storer.SetReference(headRef))

	c := &Repository{repo: r}
	err = c.Checkout("main")
	assert.NilError(t, err)

	// HEAD is symbolic; do not use r.Head() as it resolves and fails when refs/heads/main does not exist yet
	ref, err := r.Storer.Reference(plumbing.HEAD)
	assert.NilError(t, err)
	assert.Equal(t, ref.Target(), plumbing.NewBranchReferenceName("main"))
}

// TestOpenWithTokenSwitchesSSHRemoteToHTTPS asserts that when opening an existing repo
// with token auth and origin is SSH, the origin URL is switched to HTTPS.
func TestOpenWithTokenSwitchesSSHRemoteToHTTPS(t *testing.T) {
	dir := t.TempDir()
	r, err := gogit.PlainInit(dir, false)
	assert.NilError(t, err)

	sshURL := "git@github.com:user/repo.git"
	_, err = r.CreateRemote(&config.RemoteConfig{
		Name:  gogit.DefaultRemoteName,
		URLs:  []string{sshURL},
		Fetch: []config.RefSpec{config.RefSpec(fmt.Sprintf(config.DefaultFetchRefSpec, gogit.DefaultRemoteName))},
	})
	assert.NilError(t, err)

	mainRef := plumbing.ReferenceName("refs/heads/main")
	assert.NilError(t, r.CreateBranch(&config.Branch{Name: "main", Remote: gogit.DefaultRemoteName, Merge: mainRef}))
	headRef := plumbing.NewSymbolicReference(plumbing.HEAD, mainRef)
	assert.NilError(t, r.Storer.SetReference(headRef))

	repo, err := New(
		context.Background(),
		Root(dir),
		Token("test-token"),
		Author("u", "e@e.com"),
	)
	assert.NilError(t, err)

	rem, err := repo.Repo().Remote(gogit.DefaultRemoteName)
	assert.NilError(t, err)
	urls := rem.Config().URLs
	assert.Assert(t, len(urls) > 0, "origin should have URL")
	assert.Assert(t, strings.HasPrefix(urls[0], "https://"), "origin URL should be HTTPS, got %s", urls[0])
}

// TestOpenWithSSHKeyDoesNotSwitchRemote asserts that when opening with SSHKey and origin is SSH,
// the origin URL is left unchanged.
func TestOpenWithSSHKeyDoesNotSwitchRemote(t *testing.T) {
	pubKey, secKey, err := generateDeployKey()
	assert.NilError(t, err)

	dir := t.TempDir()
	r, err := gogit.PlainInit(dir, false)
	assert.NilError(t, err)

	sshURL := "git@github.com:owner/repo.git"
	_, err = r.CreateRemote(&config.RemoteConfig{
		Name:  gogit.DefaultRemoteName,
		URLs:  []string{sshURL},
		Fetch: []config.RefSpec{config.RefSpec(fmt.Sprintf(config.DefaultFetchRefSpec, gogit.DefaultRemoteName))},
	})
	assert.NilError(t, err)

	mainRef := plumbing.ReferenceName("refs/heads/main")
	assert.NilError(t, r.CreateBranch(&config.Branch{Name: "main", Remote: gogit.DefaultRemoteName, Merge: mainRef}))
	headRef := plumbing.NewSymbolicReference(plumbing.HEAD, mainRef)
	assert.NilError(t, r.Storer.SetReference(headRef))

	_ = pubKey
	repo, err := New(
		context.Background(),
		Root(dir),
		SSHKey(secKey),
		Author("u", "e@e.com"),
	)
	assert.NilError(t, err)

	rem, err := repo.Repo().Remote(gogit.DefaultRemoteName)
	assert.NilError(t, err)
	urls := rem.Config().URLs
	assert.Assert(t, len(urls) > 0, "origin should have URL")
	assert.Assert(t, strings.HasPrefix(urls[0], "git@"), "origin URL should remain SSH, got %s", urls[0])
}

// TestNewEmptyRemote asserts that New with an empty remote (no refs) runs the init path
// and Checkout succeeds so the repo is ready for the first commit.
func TestNewEmptyRemote(t *testing.T) {
	emptyBare := t.TempDir()
	_, err := gogit.PlainInit(emptyBare, true)
	assert.NilError(t, err)

	cloneDir := t.TempDir()
	absBare, err := filepath.Abs(emptyBare)
	assert.NilError(t, err)
	fileURL := "file://" + absBare

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := New(ctx,
		Root(cloneDir),
		URL(fileURL),
		Branch("main"),
	)
	assert.NilError(t, err)

	// After empty-remote init + Checkout, HEAD is symbolic to refs/heads/main (branch ref created on first commit)
	ref, err := repo.Repo().Storer.Reference(plumbing.HEAD)
	assert.NilError(t, err)
	assert.Equal(t, ref.Target(), plumbing.NewBranchReferenceName("main"))
}
