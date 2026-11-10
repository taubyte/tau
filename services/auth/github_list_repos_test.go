package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v71/github"
)

func listServer(t *testing.T, pages int, failPage int) (*githubClient, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if n == 0 {
			n = 1
		}
		if n == failPage {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		link := fmt.Sprintf(`<%s/user/repos?page=%d>; rel="last"`, srv.URL, pages)
		if n < pages {
			link = fmt.Sprintf(`<%s/user/repos?page=%d>; rel="next", `, srv.URL, n+1) + link
		}
		w.Header().Set("Link", link)
		w.Write([]byte(fmt.Sprintf(`[{"id":%d,"full_name":"me/r%d","url":"u"},{"id":%d,"full_name":"me/s%d","url":"u"}]`, n*10, n, n*10+1, n)))
	}))
	t.Cleanup(srv.Close)
	gh := github.NewClient(nil)
	gh.BaseURL, _ = url.Parse(srv.URL + "/")
	return &githubClient{Client: gh, ctx: context.Background()}, &calls
}

func TestListMyReposReadsEveryPage(t *testing.T) {
	client, calls := listServer(t, 7, 0)
	repos, err := client.ListMyRepos()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 14 || calls.Load() != 7 {
		t.Fatalf("%d repos in %d calls", len(repos), calls.Load())
	}
	if repos["70"].Name != "me/r7" {
		t.Fatalf("last page missing: %+v", repos["70"])
	}
}

// A page that fails must not look like repositories that do not exist.
func TestListMyReposFailsOnAFailedPage(t *testing.T) {
	for _, fail := range []int{1, 4} {
		client, _ := listServer(t, 7, fail)
		if repos, err := client.ListMyRepos(); err == nil {
			t.Fatalf("page %d failed, got %d repos and no error", fail, len(repos))
		}
	}
}
