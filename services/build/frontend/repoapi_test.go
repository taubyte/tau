package frontend

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/tenancy"
)

// ── a git host ───────────────────────────────────────────────────────────────

// gitHost is git's own http-backend over a directory of bare repositories, so
// what the routes fetch and push is answered by git itself. It accepts one
// basic-auth credential and answers 401 to anything else.
type gitHost struct {
	*httptest.Server
	root string
	want string

	mu       sync.Mutex
	requests []string
}

const gitUser, gitPassword = "x-access-token", testToken

func newGitHost(t *testing.T) *gitHost {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	require.NoError(t, err, "these tests run git's own http-backend")
	h := &gitHost{root: t.TempDir(), want: "Basic " + encodeBasic(gitUser, gitPassword)}
	backend := &cgi.Handler{
		Path: gitBin,
		Args: []string{"http-backend"},
		// http-backend refuses a push from a user it was not told of.
		Env: []string{"GIT_PROJECT_ROOT=" + h.root, "GIT_HTTP_EXPORT_ALL=1", "HOME=" + h.root, "REMOTE_USER=" + gitUser},
	}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.requests = append(h.requests, r.Method+" "+r.URL.Path)
		h.mu.Unlock()
		h.mu.Lock()
		want := h.want
		h.mu.Unlock()
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(h.Close)
	return h
}

func encodeBasic(user, password string) string {
	r, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	r.SetBasicAuth(user, password)
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Basic ")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func (h *gitHost) asked() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

// bare makes an empty bare repository at name, pushable over http.
func (h *gitHost) bare(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(h.root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	git(t, dir, "init", "--bare", "--initial-branch=main")
	git(t, dir, "config", "http.receivepack", "true")
	return dir
}

// seed makes name with one commit on main holding files.
func (h *gitHost) seed(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	bare := h.bare(t, name)
	work := t.TempDir()
	git(t, work, "init", "--initial-branch=main")
	for p, body := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(work, p)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(work, p), []byte(body), 0o644))
	}
	git(t, work, "add", "-A")
	git(t, work, "commit", "-m", "seed")
	git(t, work, "tag", "-a", "v1", "-m", "v1")
	git(t, work, "push", bare, "main", "v1")
	return git(t, work, "rev-parse", "HEAD")
}

// show is a file's contents on a branch of name, read by git.
func (h *gitHost) show(t *testing.T, name, rev string) string {
	t.Helper()
	return git(t, filepath.Join(h.root, name), "show", rev)
}

// ── the fake forge ───────────────────────────────────────────────────────────

type fakeRepos struct {
	host *gitHost

	listed  []RepositoryInfo
	read    Access
	err     error
	created []string
	tokens  []string
}

func (f *fakeRepos) List(_ context.Context, token, _ string) ([]RepositoryInfo, error) {
	f.tokens = append(f.tokens, token)
	return f.listed, f.err
}

func (f *fakeRepos) Create(_ context.Context, token string, key repokey.Key, in NewRepository) (RepositoryInfo, error) {
	f.tokens = append(f.tokens, token)
	if f.err != nil {
		return RepositoryInfo{}, f.err
	}
	f.created = append(f.created, key.String())
	return RepositoryInfo{Repository: key.String(), DefaultBranch: "main", Private: in.Private}, nil
}

func (f *fakeRepos) ReadAccess(_ context.Context, token string, _ repokey.Key) (Access, error) {
	f.tokens = append(f.tokens, token)
	return f.read, f.err
}

// Remote maps github.com/<owner>/<name> to <owner>/<name>.git on the git
// host, with a transport that carries the caller's forge token there.
func (f *fakeRepos) Remote(_ context.Context, token string, key repokey.Key) (Remote, error) {
	if f.err != nil {
		return Remote{}, f.err
	}
	u, err := url.Parse(f.host.URL + "/" + key.FullName() + ".git")
	if err != nil {
		return Remote{}, err
	}
	return Remote{URL: u, Author: "alice", Email: "alice@example.com", Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r.SetBasicAuth(gitUser, token)
		return http.DefaultTransport.RoundTrip(r)
	})}, nil
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func repoHarness(t *testing.T, cfg Config, opts ...Option) (*harness, *fakeRepos) {
	t.Helper()
	repos := &fakeRepos{host: newGitHost(t), read: AccessGranted}
	return newHarness(t, cfg, append([]Option{WithRepositories(repos)}, opts...)...), repos
}

func jsonCall(method, path, body string) *http.Request {
	r := authed(method, path, testToken)
	r.Body = io.NopCloser(strings.NewReader(body))
	return r
}

// ── the routes ───────────────────────────────────────────────────────────────

func TestRepositoryRoutesNeedRepositories(t *testing.T) {
	h := newHarness(t, Config{})
	for _, r := range []*http.Request{
		jsonCall(http.MethodGet, "/forges/github.com/repositories", ""),
		jsonCall(http.MethodPost, "/forges/github.com/repositories", `{}`),
		jsonCall(http.MethodPost, "/forges/github.com/read", `{}`),
		jsonCall(http.MethodPost, "/forges/github.com/commit", `{}`),
		jsonCall(http.MethodGet, "/git/github.com/acme/pay.git/info/refs?service=git-upload-pack", ""),
	} {
		require.Equal(t, http.StatusNotFound, h.do(t, r).Code, r.URL.String())
	}
	_, err := New(Config{}, WithRepositories(nil))
	require.Error(t, err)
	_, err = New(Config{GitTimeout: -1})
	require.Error(t, err)
	_, err = New(Config{MaxPushBytes: -1})
	require.Error(t, err)
}

// The listing is the forge's, less what this cloud cannot serve: a spelling
// that is not a key, another host's, and a namespace outside its tenancy.
func TestListRepositories(t *testing.T) {
	served, err := tenancy.Parse([]string{"github.com/acme"})
	require.NoError(t, err)
	h, repos := repoHarness(t, Config{}, WithTenancy(served))
	h.connections.forge = map[string]string{"github.com": "forge-token"}
	repos.listed = []RepositoryInfo{
		{Repository: "github.com/acme/pay", DefaultBranch: "main", Private: true},
		{Repository: "github.com/other/pay"},
		{Repository: "gitlab.com/acme/pay"},
		{Repository: "GitHub.com/acme/x"},
	}
	w := h.do(t, jsonCall(http.MethodGet, "/forges/github.com/repositories", ""))
	require.Equal(t, http.StatusOK, w.Code)
	var got repositoriesResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&got))
	require.Equal(t, []RepositoryInfo{{Repository: "github.com/acme/pay", DefaultBranch: "main", Private: true}}, got.Repositories)
	require.Equal(t, []string{"forge-token"}, repos.tokens, "asked with the connection, never the tau token")

	repos.listed = nil
	w = h.do(t, jsonCall(http.MethodGet, "/forges/github.com/repositories", ""))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"repositories":[]}`, bodyOf(t, w))

	for err, want := range map[error]int{
		ErrUnsupported:                http.StatusNotImplemented,
		ErrUnknownForge:               http.StatusNotFound,
		ErrBadCredential:              http.StatusUnauthorized,
		errors.New("forge fell over"): http.StatusBadGateway,
	} {
		repos.err = err
		w := h.do(t, jsonCall(http.MethodGet, "/forges/github.com/repositories", ""))
		require.Equal(t, want, w.Code, err.Error())
	}
}

func TestListRepositoriesAdmitsOnce(t *testing.T) {
	adm := &fakeAdmissions{admitted: false, reason: "ask for an invite"}
	h, repos := repoHarness(t, Config{}, WithAdmissions(adm))
	repos.listed = []RepositoryInfo{{Repository: "github.com/acme/pay"}, {Repository: "github.com/acme/web"}}
	w := h.do(t, jsonCall(http.MethodGet, "/forges/github.com/repositories", ""))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, bodyOf(t, w), "ask for an invite")
	require.Equal(t, 1, adm.asked)
}

// A caller with no connection to the forge is told to connect it; a token
// that does not verify is a 401.
func TestRepositoryRoutesOpenTheConnection(t *testing.T) {
	h, _ := repoHarness(t, Config{})
	h.connections.err = map[string]error{"github.com": &NotConnectedError{Reason: "auth: not connected to github.com: connect it first"}}
	w := h.do(t, jsonCall(http.MethodGet, "/forges/github.com/repositories", ""))
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, bodyOf(t, w), "connect it first")

	h.connections.err = map[string]error{"github.com": ErrBadCredential}
	w = h.do(t, jsonCall(http.MethodPost, "/forges/github.com/repositories", `{"repository":"github.com/acme/new"}`))
	require.Equal(t, http.StatusUnauthorized, w.Code)

	h.connections.err = map[string]error{"github.com": errors.New("auth unreachable")}
	w = h.do(t, jsonCall(http.MethodGet, "/forges/github.com/repositories", ""))
	require.Equal(t, http.StatusBadGateway, w.Code)
}

func TestCreateRepository(t *testing.T) {
	h, repos := repoHarness(t, Config{})
	w := h.do(t, jsonCall(http.MethodPost, "/forges/github.com/repositories", `{"repository":"github.com/acme/new","private":true}`))
	require.Equal(t, http.StatusCreated, w.Code)
	require.JSONEq(t, `{"repository":"github.com/acme/new","default_branch":"main","private":true}`, bodyOf(t, w))
	require.Equal(t, []string{"github.com/acme/new"}, repos.created)

	for body, want := range map[string]int{
		`nope`:                                  http.StatusBadRequest,
		`{"repository":"Not/A/Key"}`:            http.StatusBadRequest,
		`{"repository":"gitlab.com/acme/new"}`:  http.StatusBadRequest,
		`{"repository":"github.com/acme/new2"}`: http.StatusCreated,
	} {
		w := h.do(t, jsonCall(http.MethodPost, "/forges/github.com/repositories", body))
		require.Equal(t, want, w.Code, body)
	}

	repos.err = ErrUnsupported
	w = h.do(t, jsonCall(http.MethodPost, "/forges/github.com/repositories", `{"repository":"github.com/acme/new3"}`))
	require.Equal(t, http.StatusNotImplemented, w.Code)
}

// A namespace this cloud does not serve gets no repository made for it.
func TestCreateRepositoryHonoursTenancy(t *testing.T) {
	served, err := tenancy.Parse([]string{"github.com/acme"})
	require.NoError(t, err)
	h, repos := repoHarness(t, Config{}, WithTenancy(served))
	w := h.do(t, jsonCall(http.MethodPost, "/forges/github.com/repositories", `{"repository":"github.com/other/new"}`))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Empty(t, repos.created)
}
