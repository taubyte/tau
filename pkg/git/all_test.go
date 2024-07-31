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
	if tkn := os.Getenv("TEST_GIT_TOKEN"); tkn == "" {
		t.SkipNow()
	}
	return
}

func TestNew(t *testing.T) {
	_, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root("/tmp/taf"),
		Author(testRepoUser, testRepoEmail),
	)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}
}

func TestTempWithRoot(t *testing.T) {
	testRoot := "someRoot"

	repo, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root(testRoot),
		Temporary(),
		Author(testRepoUser, testRepoEmail),
	)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}

	if len(repo.Root()) == 0 {
		t.Errorf("repo.Root() got nothing")
		return
	}

	_, err = os.Stat(repo.workDir)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}

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
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}

	if len(repo.Root()) == 0 {
		t.Errorf("repo.Root() got nothing")
		return
	}

	_, err = os.Stat(repo.root)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}

	if repo.root != repo.workDir {
		t.Errorf("No root provided workdir `%s`should be the same as repo root `%s`", repo.workDir, repo.root)
		return
	}
}

func TestCommit(t *testing.T) {

	c, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root("/tmp/taf"),
		Author(testRepoUser, testRepoEmail),
	)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}
	err = os.WriteFile("/tmp/taf/plain.txt", []byte(fmt.Sprint("Some shit", time.Now())), 0755)
	if err != nil {
		t.Errorf("Unable to write file: %v", err)
		return
	}

	err = c.Commit("Adding plain file", "plain.txt")
	if err != nil {
		t.Errorf("Testing commit failed")
		return
	}
}

func TestPush(t *testing.T) {
	c, err := New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root("/tmp/taf"),
		Author(testRepoUser, testRepoEmail),
	)
	if err != nil {
		t.Errorf("Testing New failed")
		return
	}
	err = c.Push()
	if err != nil {
		t.Errorf("Testing push failed")
		return
	}
}

func TestClone(t *testing.T) {

	var tn = time.Now()
	var timenow = tn.String()

	err := os.Mkdir("/tmp/"+timenow, 0755)
	if err != nil {
		t.Errorf("Failed to create new directory with %s", err.Error())
		return
	}

	_, err = New(
		context.Background(),
		URL(testRepoHTTPUrl),
		Token(testRepoToken(t)),
		Root("/tmp/"+timenow),
		Author(testRepoUser, testRepoEmail),
	)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}

	err = os.RemoveAll("/tmp/" + timenow)
	if err != nil {
		t.Errorf("Failed to delete directory %s with %s", timenow, err.Error())
		return
	}
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
	if err != nil {
		t.Error(err)
	}

	ctx, ctxC := context.WithTimeout(context.Background(), 10*time.Second)
	defer ctxC()

	githubClient := githubApiClient(ctx, testRepoToken(t))

	err = injectDeploymentKey(ctx, githubClient, testRepoUser, testRepoName, "go-simple-git-clone-with-deploy-key", pubKey)
	if err != nil {
		t.Error(err)
	}

	var tn = time.Now()
	var timenow = tn.String()

	err = os.Mkdir("/tmp/"+timenow, 0755)
	if err != nil {
		t.Errorf("Failed to create new directory with %s", err.Error())
		return
	}

	_, err = New(
		context.Background(),
		URL(testRepoGitUrl),
		SSHKey(secKey),
		Root("/tmp/"+timenow),
		Author(testRepoUser, testRepoEmail),
	)
	if err != nil {
		t.Errorf("Testing New failed with error: %s", err.Error())
		return
	}

	err = os.RemoveAll("/tmp/" + timenow)
	if err != nil {
		t.Errorf("Failed to delete directory %s with %s", timenow, err.Error())
		return
	}
}
