package frontend

import (
	"context"
	"encoding/json"
	"errors"
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

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/pkg/register"
)

// This package's whole fixture is five fakes and an httptest recorder. There is
// no libp2p host, no listener and no port anywhere in it — which is the claim
// doc.go makes about why the dependencies are interfaces, held here as a
// test rather than as a sentence.

const (
	testHookID  = "hook-1"
	testSecret  = "s3cret"
	testRepoKey = "github.com/acme/pay"
	testProject = "proj-1"
	testJobID   = "9913daff5bd6d6b306ccef31848fe940e88a62ca8b8187435af49c5d6ee39277"
	testToken   = "tau_token"
	testUser    = "user-1"
	testSHA     = "1111111111111111111111111111111111111111"
)

// ── the fakes ────────────────────────────────────────────────────────────────

type fakeHooks struct {
	mu    sync.Mutex
	calls int
	get   func(id string) (Hook, error)
}

func (f *fakeHooks) Hook(_ context.Context, id string) (Hook, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.get != nil {
		return f.get(id)
	}
	if id != testHookID {
		return Hook{}, fmt.Errorf("%w: %s", ErrNoHook, id)
	}
	return Hook{Project: id, Repositories: []HookRepository{{Key: testRepoKey, Secret: []byte(testSecret)}}}, nil
}

func (f *fakeHooks) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeDeliveries stands in for the forge. HOW a delivery is verified is
// pkg/forge's, tested there against every scheme, and forgeauth's end-to-end
// test runs real signed deliveries through this handler; what this package owns
// is what the handler does with each answer, so the fake is the answers.
//
// By default a delivery verifies when its test signature is one repository's
// secret, and means the push its body names, for that repository.
type fakeDeliveries struct {
	verify func(hook Hook, header http.Header, body []byte) (Push, error)
}

const testSignatureHeader = "X-Test-Signature"

func (f *fakeDeliveries) Verify(hook Hook, header http.Header, body []byte) (Push, error) {
	if f.verify != nil {
		return f.verify(hook, header, body)
	}
	for _, repo := range hook.Repositories {
		if len(repo.Secret) == 0 || header.Get(testSignatureHeader) != string(repo.Secret) {
			continue
		}
		var p struct{ Ref, After string }
		if err := json.Unmarshal(body, &p); err != nil {
			return Push{}, err
		}
		return Push{Repository: repo.Key, Ref: p.Ref, Commit: p.After}, nil
	}
	return Push{}, fmt.Errorf("%w: wrong signature", ErrUnverified)
}

// fakeProjects makes testUser the owner of testProject, which testRepoKey
// belongs to, unless a test says otherwise.
type fakeProjects struct {
	role    func(project, user string) (register.Role, error)
	project func(key string) (string, error)
}

func (f *fakeProjects) Role(_ context.Context, project, user string) (register.Role, error) {
	if f.role != nil {
		return f.role(project, user)
	}
	if project != testProject {
		return register.RoleNone, connect.NewError(connect.CodeNotFound, fmt.Errorf("no project %s", project))
	}
	if user == testUser {
		return register.RoleOwner, nil
	}
	return register.RoleNone, nil
}

func (f *fakeProjects) Project(_ context.Context, key string) (string, error) {
	if f.project != nil {
		return f.project(key)
	}
	if key != testRepoKey {
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("no repository %s", key))
	}
	return testProject, nil
}

// fakeCallers verifies testToken as testUser and refuses anything else.
type fakeCallers struct {
	caller func(token string) (string, error)
}

func (f *fakeCallers) Caller(_ context.Context, token string) (string, error) {
	if f.caller != nil {
		return f.caller(token)
	}
	if token != testToken {
		return "", ErrBadCredential
	}
	return testUser, nil
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
	get     func(id string) (*buildv2.Job, error)
	list    func(projectID string, emit func(string) error) error
	cancel  func(id string) error
	retry   func(id string) error
	follow  func(id string, from int64, emit func([]byte, int64) error) (int64, error)
}

func (f *fakeBackend) Follow(ctx context.Context, id string, from int64, emit func([]byte, int64) error) (int64, error) {
	f.mu.Lock()
	f.ctx = ctx
	f.mu.Unlock()
	if f.follow != nil {
		return f.follow(id, from, emit)
	}
	return from, connect.NewError(connect.CodeUnavailable, errors.New("no build of that job is running"))
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

func (f *fakeBackend) Get(ctx context.Context, id string) (*buildv2.Job, error) {
	f.mu.Lock()
	f.ctx = ctx
	f.mu.Unlock()
	if f.get != nil {
		return f.get(id)
	}
	if id != testJobID {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("no job %s", id))
	}
	return &buildv2.Job{
		Id:            id,
		Status:        buildv2.JobStatus_JobStatusSuccess,
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

// ── the service under test ───────────────────────────────────────────────────

type harness struct {
	*Service
	hooks      *fakeHooks
	deliveries *fakeDeliveries
	projects   *fakeProjects
	callers    *fakeCallers
	backend    *fakeBackend
	clock      *clock
	logs       *logs
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
		hooks:      &fakeHooks{},
		deliveries: &fakeDeliveries{},
		projects:   &fakeProjects{},
		callers:    &fakeCallers{},
		backend:    &fakeBackend{},
		clock:      &clock{at: time.Unix(1700000000, 0).UTC()},
		logs:       &logs{},
	}

	opts = append([]Option{WithClock(h.clock.now), WithLogger(h.logs.write)}, opts...)
	svc, err := New(cfg, append([]Option{WithHooks(h.hooks), WithDeliveries(h.deliveries), WithProjects(h.projects), WithBackend(h.backend), WithCallers(h.callers)}, opts...)...)
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

// pushBody is what the fake reads a push out of.
func pushBody(ref, after string) []byte {
	body, err := json.Marshal(map[string]any{"ref": ref, "after": after})
	if err != nil {
		panic(err)
	}
	return body
}

type webhookOpts struct {
	hookID      string
	contentType string
	signature   string
	body        []byte
	omitSig     bool
}

func webhookRequest(o webhookOpts) *http.Request {
	if o.hookID == "" {
		o.hookID = testHookID
	}
	if o.contentType == "" {
		o.contentType = "application/json"
	}
	if o.body == nil {
		o.body = pushBody("refs/heads/main", testSHA)
	}
	if o.signature == "" && !o.omitSig {
		o.signature = testSecret
	}

	r := httptest.NewRequest(http.MethodPost, "/hook/"+o.hookID, strings.NewReader(string(o.body)))
	r.Header.Set("Content-Type", o.contentType)
	if o.signature != "" {
		r.Header.Set(testSignatureHeader, o.signature)
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
