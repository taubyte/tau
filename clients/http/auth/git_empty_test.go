package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	git "github.com/taubyte/tau/clients/http/auth/git"
	"github.com/taubyte/tau/clients/http/auth/git/common"
)

func commitsGitHub(t *testing.T, exists bool, commits int) common.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"me"}`))
		case r.URL.Path == "/repos/me/r" && exists:
			w.Write([]byte(`{"id":7,"name":"r"}`))
		case r.URL.Path == "/repos/me/r/commits" && exists:
			w.WriteHeader(commits)
			if commits == http.StatusOK {
				w.Write([]byte(`[{"sha":"abc"}]`))
			} else {
				w.Write([]byte(`{"message":"Git Repository is empty."}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	g, err := git.New(context.Background(), "github", "token")
	if err != nil {
		t.Fatal(err)
	}
	gh, _ := g.GithubTODO()
	gh.BaseURL, _ = url.Parse(srv.URL + "/")
	return g
}

// Only a repository with no commits can be taken over by a rerun.
func TestEmptyRepository(t *testing.T) {
	ctx := context.Background()
	if id := emptyRepository(ctx, commitsGitHub(t, true, http.StatusConflict), "r"); id != "7" {
		t.Fatalf("an empty repository was not found: %q", id)
	}
	if id := emptyRepository(ctx, commitsGitHub(t, true, http.StatusOK), "r"); id != "" {
		t.Fatalf("a repository with commits was adopted: %q", id)
	}
	if id := emptyRepository(ctx, commitsGitHub(t, false, 0), "r"); id != "" {
		t.Fatalf("a missing repository was found: %q", id)
	}
	// a GitHub failure says nothing about the repository
	if id := emptyRepository(ctx, commitsGitHub(t, true, http.StatusBadGateway), "r"); id != "" {
		t.Fatalf("a failed check was taken as empty: %q", id)
	}
}

// Only a repository seen to have commits counts: a failed check says nothing.
func TestRepositoryHasCommits(t *testing.T) {
	ctx := context.Background()
	if !repositoryHasCommits(ctx, commitsGitHub(t, true, http.StatusOK), "r") {
		t.Fatal("a repository with commits was not seen")
	}
	if repositoryHasCommits(ctx, commitsGitHub(t, true, http.StatusConflict), "r") {
		t.Fatal("an empty repository was said to have commits")
	}
	if repositoryHasCommits(ctx, commitsGitHub(t, false, 0), "r") {
		t.Fatal("a missing repository was said to have commits")
	}
	if repositoryHasCommits(ctx, commitsGitHub(t, true, http.StatusBadGateway), "r") {
		t.Fatal("a failed check was taken as commits")
	}
}
