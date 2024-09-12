package git

import (
	"context"
	"fmt"
	"os"
	"path"
	"testing"
	"time"

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
