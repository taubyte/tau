package service

import (
	"context"
	"errors"
	goHttp "net/http"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/google/go-github/v71/github"
	commonIface "github.com/taubyte/tau/core/services/patrick"
	http "github.com/taubyte/tau/pkg/http"
	httpAuth "github.com/taubyte/tau/pkg/http/auth"
	authService "github.com/taubyte/tau/services/auth"
)

// fakeRepos answers GetByID with the permissions a test gives each repository.
type fakeRepos struct {
	authService.GitHubClient
	perms map[string]map[string]bool
	asked int
	cur   *github.Repository
	fail  error // GetByID fails like this, when set
}

func (f *fakeRepos) GetByID(id string) error {
	f.asked++
	if f.fail != nil {
		return f.fail
	}
	p, ok := f.perms[id]
	if !ok {
		return &github.ErrorResponse{Response: &goHttp.Response{StatusCode: goHttp.StatusNotFound}}
	}
	f.cur = &github.Repository{Permissions: p}
	return nil
}

func (f *fakeRepos) Cur() *github.Repository { return f.cur }

type listJobs struct {
	*fakeJobs
	listed map[string][]string
}

func (d *listJobs) List(_ context.Context, prefix string) ([]string, error) {
	return d.listed[prefix], nil
}

func addJob(t *testing.T, db *fakeJobs, loc, id string, repo int) {
	t.Helper()
	job := &commonIface.Job{Id: id}
	job.Meta.Repository.ID = repo
	data, err := cbor.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	db.keys[loc+id] = data
}

func accessCtx(token string, client authService.GitHubClient, vars map[string]any) *mockHTTPContext {
	ctx := newMockHTTPContext()
	ctx.SetVariable("Authorization", httpAuth.Authorization{Type: "github", Token: token})
	if client != nil {
		ctx.SetVariable("GithubClient", client)
	}
	for k, v := range vars {
		ctx.SetVariable(k, v)
	}
	return ctx
}

func TestJobAccess(t *testing.T) {
	db := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, db, "/archive/jobs/", "done", 1)
	addJob(t, db, "/jobs/", "running", 1)
	addJob(t, db, "/archive/jobs/", "other", 2)
	srv := &PatrickService{db: db}

	ran := 0
	handler := srv.withJobAccess("jid", func(http.Context) (interface{}, error) { ran++; return "ok", nil })
	client := &fakeRepos{perms: map[string]map[string]bool{
		"1": {"push": true},
		"2": {"pull": true}, // a public repository reads to everyone
	}}

	for _, c := range []struct {
		name, jid string
		client    authService.GitHubClient
		want      error
	}{
		{"finished job, writer", "done", client, nil},
		{"running job, writer", "running", client, nil},
		{"read only is refused", "other", client, errNoJobAccess},
		{"no client is refused", "done", nil, errNoJobAccess},
		{"unknown job reaches the handler", "nope", client, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := ran
			_, err := handler(accessCtx("token-"+c.name, c.client, map[string]any{"jid": c.jid}))
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if (ran > before) != (c.want == nil) {
				t.Fatalf("handler ran=%v with err %v", ran > before, err)
			}
		})
	}
}

func TestJobAccessAsksGitHubOncePerRepository(t *testing.T) {
	db := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, db, "/archive/jobs/", "a", 11)
	addJob(t, db, "/archive/jobs/", "b", 11)
	srv := &PatrickService{db: db}
	handler := srv.withJobAccess("jid", func(http.Context) (interface{}, error) { return "ok", nil })
	client := &fakeRepos{perms: map[string]map[string]bool{"11": {"push": true}}}

	for _, jid := range []string{"a", "b", "a"} {
		if _, err := handler(accessCtx("cached-token", client, map[string]any{"jid": jid})); err != nil {
			t.Fatal(err)
		}
	}
	if client.asked != 1 {
		t.Fatalf("asked GitHub %d times", client.asked)
	}

	// the answer belongs to that token, not to the repository
	stranger := &fakeRepos{perms: map[string]map[string]bool{"11": {"pull": true}}}
	if _, err := handler(accessCtx("another-token", stranger, map[string]any{"jid": "a"})); !errors.Is(err, errNoJobAccess) {
		t.Fatalf("got %v", err)
	}
}

func TestProjectAccess(t *testing.T) {
	jobs := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, jobs, "/archive/jobs/", "config", 21)
	addJob(t, jobs, "/archive/jobs/", "code", 22)
	db := &listJobs{fakeJobs: jobs, listed: map[string][]string{
		"/by/project/p/": {"/by/project/p/config", "/by/project/p/code"},
	}}
	srv := &PatrickService{db: db}
	handler := srv.withProjectAccess(func(http.Context) (interface{}, error) { return "ok", nil })

	for _, c := range []struct {
		name, project string
		perms         map[string]map[string]bool
		want          error
	}{
		{"writer of one repository", "p", map[string]map[string]bool{"22": {"push": true}}, nil},
		{"stranger", "p", map[string]map[string]bool{"21": {"pull": true}}, errNoJobAccess},
		{"project without jobs", "empty", nil, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := handler(accessCtx("project-"+c.name, &fakeRepos{perms: c.perms}, map[string]any{"projectId": c.project}))
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestLiveLogsResetStartsARetriedLogClean(t *testing.T) {
	ll := &liveLogs{jobs: map[string]*liveLog{}}
	ll.add(chunk(0, "old", false))
	ll.add(chunk(3, "", true))
	ll.reset("j")
	if ll.jobs["j"] != nil || ll.total != 0 {
		t.Fatalf("left %v total=%d", ll.jobs["j"], ll.total)
	}
	ll.reset("unknown")
}

// GitHub failing to answer must not lock the owner out for the cache's lifetime.
func TestJobAccessDoesNotKeepADenialGitHubDidNotGive(t *testing.T) {
	db := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, db, "/archive/jobs/", "j", 31)
	srv := &PatrickService{db: db}
	handler := srv.withJobAccess("jid", func(http.Context) (interface{}, error) { return "ok", nil })

	flaky := &fakeRepos{perms: map[string]map[string]bool{"31": {"push": true}}}
	flaky.fail = &github.ErrorResponse{Response: &goHttp.Response{StatusCode: goHttp.StatusBadGateway}}
	if _, err := handler(accessCtx("flaky-token", flaky, map[string]any{"jid": "j"})); !errors.Is(err, errNoJobAccess) {
		t.Fatalf("got %v while GitHub failed", err)
	}

	flaky.fail = nil
	if _, err := handler(accessCtx("flaky-token", flaky, map[string]any{"jid": "j"})); err != nil {
		t.Fatalf("owner still locked out after GitHub recovered: %v", err)
	}

	// GitHub's own no is kept
	stranger := &fakeRepos{perms: map[string]map[string]bool{}}
	handler(accessCtx("stranger-token", stranger, map[string]any{"jid": "j"}))
	handler(accessCtx("stranger-token", stranger, map[string]any{"jid": "j"}))
	if stranger.asked != 1 {
		t.Fatalf("a certain denial was asked %d times", stranger.asked)
	}
}
