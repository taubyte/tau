package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/avast/retry-go/v4"
	git "github.com/taubyte/tau/clients/http/auth/git"
	"github.com/taubyte/tau/clients/http/auth/git/common"
)

// fakeGitHub answers POST /user/repos with the given statuses in turn; existing
// says whether GET of the repository finds it.
func fakeGitHub(t *testing.T, statuses []int, existing bool) (common.Client, *atomic.Int32) {
	t.Helper()
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/user/repos":
			i := int(posts.Add(1)) - 1
			if i >= len(statuses) {
				i = len(statuses) - 1
			}
			w.WriteHeader(statuses[i])
			if statuses[i] == http.StatusCreated {
				w.Write([]byte(`{"id":7,"name":"r"}`))
			}
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"me"}`))
		case r.URL.Path == "/repos/me/r" && existing:
			w.Write([]byte(`{"id":7,"name":"r"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	g, err := git.New(context.Background(), "github", "token")
	if err != nil {
		t.Fatal(err)
	}
	gh, err := g.GithubTODO()
	if err != nil {
		t.Fatal(err)
	}
	gh.BaseURL, _ = url.Parse(srv.URL + "/")
	return g, &posts
}

func TestCreateRepository(t *testing.T) {
	opts := []retry.Option{retry.Attempts(3), retry.Delay(time.Millisecond)}

	t.Run("created", func(t *testing.T) {
		g, posts := fakeGitHub(t, []int{http.StatusCreated}, false)
		repo, err := createRepository(context.Background(), g, "r", "", true, opts...)
		if err != nil || repo.Get().ID() != "7" || posts.Load() != 1 {
			t.Fatalf("repo=%v err=%v posts=%d", repo, err, posts.Load())
		}
	})

	t.Run("server error but the repository was made", func(t *testing.T) {
		g, posts := fakeGitHub(t, []int{http.StatusBadGateway}, true)
		repo, err := createRepository(context.Background(), g, "r", "", true, opts...)
		if err != nil || repo.Get().ID() != "7" {
			t.Fatalf("repo=%v err=%v", repo, err)
		}
		if posts.Load() != 1 {
			t.Fatalf("asked to create it %d times", posts.Load())
		}
	})

	t.Run("server error then created", func(t *testing.T) {
		g, _ := fakeGitHub(t, []int{http.StatusBadGateway, http.StatusCreated}, false)
		repo, err := createRepository(context.Background(), g, "r", "", true, opts...)
		if err != nil || repo.Get().ID() != "7" {
			t.Fatalf("repo=%v err=%v", repo, err)
		}
	})

	t.Run("a name already taken is not adopted", func(t *testing.T) {
		g, _ := fakeGitHub(t, []int{http.StatusUnprocessableEntity}, true)
		if repo, err := createRepository(context.Background(), g, "r", "", true, opts...); err == nil {
			t.Fatalf("adopted an existing repository: %v", repo)
		}
	})

	t.Run("server errors and no repository", func(t *testing.T) {
		g, _ := fakeGitHub(t, []int{http.StatusBadGateway}, false)
		if repo, err := createRepository(context.Background(), g, "r", "", true, opts...); err == nil {
			t.Fatalf("got %v", repo)
		}
	})
}
