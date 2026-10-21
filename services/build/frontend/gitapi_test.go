package frontend

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
)

// gitThrough runs git against the frontend's /git/ remote, as a browser's
// git does: with the tau bearer and nothing else.
func gitThrough(t *testing.T, dir, bearer string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "http.extraHeader=Authorization: Bearer " + bearer, "-c", "credential.helper="}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A clone, a pull and a push of a real repository, through the frontend,
// under the caller's connection — over both protocol versions.
func TestGitThroughTheFrontend(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	head := repos.host.seed(t, "acme/pay.git", map[string]string{"README": "hi"})
	front := httptest.NewServer(h.Handler())
	defer front.Close()
	remote := front.URL + "/git/github.com/acme/pay.git"

	for _, version := range []string{"0", "2"} {
		work := t.TempDir()
		out, err := gitThrough(t, work, testToken, "-c", "protocol.version="+version, "clone", remote, "repo")
		require.NoError(t, err, out)
		got, err := os.ReadFile(filepath.Join(work, "repo", "README"))
		require.NoError(t, err)
		require.Equal(t, "hi", string(got))
		require.Equal(t, head, git(t, filepath.Join(work, "repo"), "rev-parse", "HEAD"))
	}

	work := t.TempDir()
	out, err := gitThrough(t, work, testToken, "clone", remote, "repo")
	require.NoError(t, err, out)
	dir := filepath.Join(work, "repo")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "new"), []byte("x"), 0o644))
	git(t, dir, "add", "new")
	git(t, dir, "commit", "-m", "pushed through")
	out, err = gitThrough(t, dir, testToken, "push", "origin", "main")
	require.NoError(t, err, out)
	require.Equal(t, git(t, dir, "rev-parse", "HEAD"), git(t, repos.host.root+"/acme/pay.git", "rev-parse", "main"))
	out, err = gitThrough(t, dir, testToken, "pull", "--ff-only")
	require.NoError(t, err, out)
}

// Pushing takes write access and fetching takes read; a denial reads nothing
// and pushes nothing.
func TestGitIsAuthorized(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	repos.host.seed(t, "acme/pay.git", map[string]string{"README": "hi"})
	h.identity.write = func(string, repokey.Key) (Access, error) { return AccessDenied, nil }
	front := httptest.NewServer(h.Handler())
	defer front.Close()
	remote := front.URL + "/git/github.com/acme/pay.git"

	work := t.TempDir()
	out, err := gitThrough(t, work, testToken, "clone", remote, "repo")
	require.NoError(t, err, "reading takes read: %s", out)
	dir := filepath.Join(work, "repo")
	git(t, dir, "commit", "--allow-empty", "-m", "nope")
	out, err = gitThrough(t, dir, testToken, "push", "origin", "main")
	require.Error(t, err, out)
	require.NotEqual(t, git(t, dir, "rev-parse", "HEAD"), git(t, repos.host.root+"/acme/pay.git", "rev-parse", "main"))

	denied, _ := repoHarness(t, Config{})
	denied.Service.repos = &fakeRepos{host: repos.host, read: AccessDenied}
	front2 := httptest.NewServer(denied.Handler())
	defer front2.Close()
	before := repos.host.asked()
	out, err = gitThrough(t, t.TempDir(), testToken, "clone", front2.URL+"/git/github.com/acme/pay.git", "repo")
	require.Error(t, err, out)
	require.Equal(t, before, repos.host.asked(), "the forge was never asked")
}

func TestGitRoutes(t *testing.T) {
	h, repos := repoHarness(t, Config{MaxPushBytes: 16})
	repos.host.seed(t, "acme/pay.git", map[string]string{"README": "hi"})

	for target, want := range map[string]int{
		"/git/github.com/acme/pay.git/info/refs?service=git-upload-pack": http.StatusOK,
		"/git/github.com/acme/pay.git/info/refs":                         http.StatusBadRequest,
		"/git/github.com/acme/pay.git/info/refs?service=other":           http.StatusBadRequest,
		"/git/github.com/acme/pay.git/HEAD":                              http.StatusNotFound,
		"/git/github.com/acme/pay/info/refs":                             http.StatusNotFound,
		"/git/GitHub.com/acme/pay.git/info/refs":                         http.StatusNotFound,
	} {
		w := h.do(t, authed(http.MethodGet, target, testToken))
		require.Equal(t, want, w.Code, target)
	}
	w := h.do(t, authed(http.MethodGet, "/git/github.com/acme/pay.git/info/refs?service=git-upload-pack", testToken))
	require.Equal(t, "application/x-git-upload-pack-advertisement", w.Header().Get("Content-Type"))
	require.Contains(t, bodyOf(t, w), "refs/heads/main")

	w = h.do(t, authed(http.MethodPost, "/git/github.com/acme/pay.git/info/refs", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)

	push := jsonCall(http.MethodPost, "/git/github.com/acme/pay.git/git-receive-pack", strings.Repeat("0", 64))
	push.Header.Set("Content-Type", "application/x-git-receive-pack-request")
	w = h.do(t, push)
	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	// No bearer, no git.
	w = h.do(t, authed(http.MethodGet, "/git/github.com/acme/pay.git/info/refs?service=git-upload-pack", ""))
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

// What the forge answers that a browser must not act on is said in this
// cloud's terms: a redirect is an upstream failure, a refused credential a
// 403 with no WWW-Authenticate, and a transport failure a 502.
func TestGitUpstreamAnswers(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	target := "/git/github.com/acme/pay.git/info/refs?service=git-upload-pack"

	repos.host.mu.Lock()
	repos.host.want = "something else"
	repos.host.mu.Unlock()
	w := h.do(t, authed(http.MethodGet, target, testToken))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, w.Header().Get("WWW-Authenticate"))

	redirect := httptest.NewServer(http.RedirectHandler("https://elsewhere.example/", http.StatusFound))
	defer redirect.Close()
	repos.host.Server.Config.Handler = redirect.Config.Handler
	w = h.do(t, authed(http.MethodGet, target, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)

	repos.host.Close()
	w = h.do(t, authed(http.MethodGet, target, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)

	repos.err = errors.New("no remote")
	w = h.do(t, authed(http.MethodGet, target, testToken))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

// A browser's git sends Git-Protocol, and the preflight allows it.
func TestCORSAllowsGitProtocol(t *testing.T) {
	h := newHarness(t, Config{Origins: []string{"https://console.example"}})
	r := httptest.NewRequest(http.MethodOptions, "/git/github.com/acme/pay.git/info/refs", nil)
	r.Header.Set("Origin", "https://console.example")
	r.Header.Set("Access-Control-Request-Method", http.MethodGet)
	w := h.do(t, r)
	require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Git-Protocol")
}

// A deeply nested key is a repository like any other, and the bound on the
// path is the git routes' own, not an id's.
func TestGitReachesADeepKey(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	deep := "acme/" + strings.Repeat("group/", 30) + "pay"
	repos.host.seed(t, deep+".git", map[string]string{"README": "hi"})
	w := h.do(t, authed(http.MethodGet, "/git/github.com/"+deep+".git/info/refs?service=git-upload-pack", testToken))
	require.Equal(t, http.StatusOK, w.Code)
	w = h.do(t, authed(http.MethodGet, "/git/github.com/"+strings.Repeat("a/", maxGitPath)+"x.git/info/refs?service=git-upload-pack", testToken))
	require.Equal(t, http.StatusNotFound, w.Code)
}
