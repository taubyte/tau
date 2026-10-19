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
	gossh "golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"
)

var (
	testRepoGitUrl = "git@github.com:taubyte-test/for-tests.git"
	testRepoUser   = "taubyte-test"
	testRepoName   = "for-tests"
	testRepoEmail  = "taubytetest@gmail.com"
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
		URL(localRemote(t)),
		Root(t.TempDir()),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)
}

func TestTempWithRoot(t *testing.T) {
	testRoot := "repo"

	repo, err := New(
		context.Background(),
		URL(localRemote(t)),
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
		URL(localRemote(t)),
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
		URL(localRemote(t)),
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
		URL(localRemote(t)),
		Root(root),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

	assert.NilError(t, os.WriteFile(root+"/timestamp.txt", []byte(time.Now().String()), 0640))

	assert.NilError(t, c.Commit(t.Name(), "."))

	assert.NilError(t, c.Push())
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
		SSHKeyAs("git", secKey, githubHostKeys(t)),
		Root(t.TempDir()),
		Author(testRepoUser, testRepoEmail),
	)
	assert.NilError(t, err)

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

// TestOpenWithSSHKeyKeepsTheRemote asserts that opening an existing clone
// leaves its origin as it was: the transport is the caller's, never rewritten.
func TestOpenWithSSHKeyKeepsTheRemote(t *testing.T) {
	_, secKey, err := generateDeployKey()
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

	repo, err := New(
		context.Background(),
		Root(dir),
		SSHKeyAs("git", secKey, githubHostKeys(t)),
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

// githubHostKeys are github.com's published ssh host keys (api.github.com/meta),
// for the tests that clone from it: a clone pins the host it is answered by.
func githubHostKeys(t *testing.T) []gossh.PublicKey {
	t.Helper()
	var out []gossh.PublicKey
	for _, line := range []string{
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
		"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=",
		"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=",
	} {
		key, _, _, _, err := gossh.ParseAuthorizedKey([]byte(line))
		assert.NilError(t, err)
		out = append(out, key)
	}
	return out
}
