package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v71/github"
)

// fakeRepoGitHub answers GET /repositories/{id} with the token as the repo name
// and push permission while push is set, counting calls.
func fakeRepoGitHub(t *testing.T, push *atomic.Bool) (*httptest.Server, *atomic.Int32) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		tok := r.Header.Get("Authorization")
		fmt.Fprintf(w, `{"id":7,"name":%q,"full_name":"o/%s","url":"u","permissions":{"push":%v}}`, tok, tok, push.Load())
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func repoClient(srv *httptest.Server, token string) *githubClient {
	c := github.NewClient(nil).WithAuthToken(token)
	c.BaseURL, _ = url.Parse(srv.URL + "/")
	return &githubClient{Client: c, Token: token, ctx: context.Background()}
}

func TestRepoLookupsAreCachedPerToken(t *testing.T) {
	var push atomic.Bool
	push.Store(true)
	srv, calls := fakeRepoGitHub(t, &push)

	// what GET /projects/{id} asks: access to config and code, then their info
	for range 100 {
		c := repoClient(srv, "tokA") // a new client per request, as http_auth builds
		if !repoAccess(c, "7") {
			t.Fatal("access refused")
		}
		if c.ShortRepositoryInfo("7").Error != "" {
			t.Fatal("info failed")
		}
	}
	if n := calls.Load(); n > 2 {
		t.Fatalf("100 requests made %d GitHub calls", n)
	}

	// another token never sees tokA's answer
	if got := repoClient(srv, "tokB").ShortRepositoryInfo("7").Name; got != "Bearer tokB" {
		t.Fatalf("tokB got %q", got)
	}
}

func TestRepoLookupExpires(t *testing.T) {
	old := repoTTL
	defer func() { repoTTL = old }()
	repoTTL = 50 * time.Millisecond

	var push atomic.Bool
	push.Store(true)
	srv, _ := fakeRepoGitHub(t, &push)

	if !repoAccess(repoClient(srv, "tokC"), "7") {
		t.Fatal("access refused")
	}
	push.Store(false) // collaborator removed
	time.Sleep(100 * time.Millisecond)
	if repoAccess(repoClient(srv, "tokC"), "7") {
		t.Fatal("removed collaborator still allowed after the TTL")
	}
}
