package client

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gotest.tools/v3/assert"
)

func testGitToken() string {
	token := os.Getenv("TEST_GIT_TOKEN")

	if token == "" {
		panic("TEST_GIT_TOKEN not set")
	}

	return token
}

// This creates a real repository on github.
func TestCreateRepo(t *testing.T) {
	var (
		authUrl       = mockAuthUrl
		gitProvider   = "github"
		tokenUsername = "taubyte-test"
		newRepoName   = "some_123_repo"
		isPrivate     = false
	)
	ctx := context.Background()
	client, err := New(ctx, URL(authUrl), Auth(testGitToken()), Provider(gitProvider))
	assert.NilError(t, err)

	_id, err := client.CreateRepository(newRepoName, "test_description", isPrivate)
	assert.NilError(t, err)

	repo, err := client.GetRepositoryById(_id)
	if err != nil {
		t.Errorf("failed to get repo: %s", err.Error())
	}

	assert.Equal(t, repo.Get().ID(), _id)
	assert.Equal(t, repo.Get().Name(), newRepoName)
	assert.Equal(t, repo.Get().Fullname(), fmt.Sprintf("%s/%s", tokenUsername, newRepoName))
	assert.Equal(t, repo.Get().Private(), isPrivate)

	// Clean up by deleting the repo
	githubClient, err := client.Git().GithubTODO()
	assert.NilError(t, err)

	gitResponse, err := githubClient.Repositories.Delete(client.ctx, tokenUsername, newRepoName)
	if err != nil {
		t.Error(err)
		fmt.Printf("Response to delete:\n%#v\n", gitResponse)
	}
}
