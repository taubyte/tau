package frontend

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/git/repokey"
	authv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/auth/v1"
	buildv1 "github.com/taubyte/tau/pkg/v2/proto/gen/tau/build/v1"
)

// This package's whole fixture is four fakes and an httptest recorder. There is
// no libp2p host, no listener and no port anywhere in it — which is the claim
// doc.go makes about why the four dependencies are interfaces, held here as a
// test rather than as a sentence.

const (
	testHookID  = "hook-1"
	testSecret  = "s3cret"
	testRepoKey = "github.com/acme/pay"
	testProject = "proj-1"
	testJobID   = "mRPa_1vW1rMGzO8xhI_pQOiKYsqLgYdDWvScXW7jknc"
	testToken   = "gho_token"
	testSHA     = "1111111111111111111111111111111111111111"
)

// ── the four fakes ───────────────────────────────────────────────────────────

type fakeHooks struct {
	mu    sync.Mutex
	calls int
	get   func(id string) (*authv1.GithubHook, error)
}

func (f *fakeHooks) Get(_ context.Context, id string) (*authv1.GithubHook, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.get != nil {
		return f.get(id)
	}
	if id != testHookID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no hook %s", id))
	}
	return &authv1.GithubHook{Id: id, Secret: testSecret, RepositoryKey: testRepoKey}, nil
}

func (f *fakeHooks) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeProjects struct {
	repositories func(id string) ([]string, error)
}

func (f *fakeProjects) Repositories(_ context.Context, id string) ([]string, error) {
	if f.repositories != nil {
		return f.repositories(id)
	}
	if id != testProject {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %s", id))
	}
	return []string{testRepoKey}, nil
}

type enqueued struct {
	repositoryKey string
	ref           string
	commitSHA     string
}

type fakeBackend struct {
	mu   sync.Mutex
	sent []enqueued
	ctx  context.Context

	enqueue func(key, ref, sha string) (string, error)
	get     func(id string) (*buildv1.Job, error)
	list    func(projectID string, emit func(string) error) error
	cancel  func(id string) error
	retry   func(id string) error
}

func (f *fakeBackend) Enqueue(ctx context.Context, key, ref, sha string) (string, error) {
	f.mu.Lock()
	f.sent = append(f.sent, enqueued{key, ref, sha})
	f.ctx = ctx
	f.mu.Unlock()
	if f.enqueue != nil {
		return f.enqueue(key, ref, sha)
	}
	return testJobID, nil
}

func (f *fakeBackend) Get(ctx context.Context, id string) (*buildv1.Job, error) {
	f.mu.Lock()
	f.ctx = ctx
	f.mu.Unlock()
	if f.get != nil {
		return f.get(id)
	}
	if id != testJobID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no job %s", id))
	}
	return &buildv1.Job{
		Id:            id,
		Status:        buildv1.JobStatus_JobStatusSuccess,
		RepositoryKey: testRepoKey,
		Ref:           "refs/heads/main",
		CommitSha:     testSHA,
		Timestamp:     1700000000,
		Attempt:       1,
		Logs:          map[string]string{"2026-01-01T00:00:00Z": "bafylog"},
		AssetCid:      map[string]string{"resource-1": "bafyasset"},
	}, nil
}

func (f *fakeBackend) List(_ context.Context, projectID string, emit func(string) error) error {
	if f.list != nil {
		return f.list(projectID, emit)
	}
	return emit(testJobID)
}

func (f *fakeBackend) Cancel(_ context.Context, id string) error {
	if f.cancel != nil {
		return f.cancel(id)
	}
	return nil
}

func (f *fakeBackend) Retry(_ context.Context, id string) error {
	if f.retry != nil {
		return f.retry(id)
	}
	return nil
}

func (f *fakeBackend) lastContext() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctx
}

func (f *fakeBackend) enqueues() []enqueued {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]enqueued(nil), f.sent...)
}

type fakeIdentity struct {
	write func(token string, key repokey.Key) (Access, error)
}

func (f *fakeIdentity) WriteAccess(_ context.Context, token string, key repokey.Key) (Access, error) {
	if f.write != nil {
		return f.write(token, key)
	}
	if token == testToken {
		return AccessGranted, nil
	}
	return AccessDenied, nil
}

// ── the service under test ───────────────────────────────────────────────────

type harness struct {
	*Service
	hooks    *fakeHooks
	projects *fakeProjects
	backend  *fakeBackend
	identity *fakeIdentity
	clock    *clock
	logs     *logs
}

type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(d)
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) write(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) joined() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func newHarness(t *testing.T, cfg Config, opts ...Option) *harness {
	t.Helper()

	h := &harness{
		hooks:    &fakeHooks{},
		projects: &fakeProjects{},
		backend:  &fakeBackend{},
		identity: &fakeIdentity{},
		clock:    &clock{at: time.Unix(1700000000, 0).UTC()},
		logs:     &logs{},
	}

	opts = append([]Option{WithClock(h.clock.now), WithLogger(h.logs.write)}, opts...)
	svc, err := New(h.hooks, h.projects, h.backend, h.identity, cfg, opts...)
	require.NoError(t, err)
	h.Service = svc
	return h
}

// do runs one request through the real mux and returns the recorder.
func (h *harness) do(t *testing.T, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, r)
	return w
}

// ── request builders ─────────────────────────────────────────────────────────

// pushBody is a GitHub push payload with only the fields this role reads.
func pushBody(cloneURL, ref, after string) []byte {
	body, err := json.Marshal(map[string]any{
		"ref":    ref,
		"after":  after,
		"before": "0000000000000000000000000000000000000000",
		// Present on purpose: nothing below must read them, and a change that
		// starts to would have to touch push.go's fixture as well as its code.
		"commits":    []map[string]any{{"added": []string{"a.go"}, "modified": []string{"b.go"}}},
		"pushed_at":  1700000000,
		"repository": map[string]any{"id": 42, "clone_url": cloneURL, "ssh_url": "git@github.com:acme/pay.git"},
	})
	if err != nil {
		panic(err)
	}
	return body
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

type webhookOpts struct {
	hookID      string
	event       string
	contentType string
	signature   string
	body        []byte
	omitSig     bool
}

func webhookRequest(o webhookOpts) *http.Request {
	if o.hookID == "" {
		o.hookID = testHookID
	}
	if o.event == "" {
		o.event = "push"
	}
	if o.contentType == "" {
		o.contentType = "application/json"
	}
	if o.body == nil {
		o.body = pushBody("https://github.com/acme/pay.git", "refs/heads/main", testSHA)
	}
	if o.signature == "" && !o.omitSig {
		o.signature = sign(testSecret, o.body)
	}

	r := httptest.NewRequest(http.MethodPost, "/github/"+o.hookID, strings.NewReader(string(o.body)))
	r.Header.Set(eventHeader, o.event)
	r.Header.Set("Content-Type", o.contentType)
	if o.signature != "" {
		r.Header.Set(signatureHeader, o.signature)
	}
	return r
}

func authed(method, path, token string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", bearerPrefix+token)
	}
	return r
}

func bodyOf(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	b, err := io.ReadAll(w.Body)
	require.NoError(t, err)
	return string(b)
}
