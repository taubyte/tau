package service

import (
	"context"
	"crypto/sha256"
	"errors"
	goHttp "net/http"
	"slices"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/google/go-github/v71/github"
	ds "github.com/ipfs/go-datastore"
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
	freshRepoAccesses(t)
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

func TestProjectListingOnlyShowsJobsTheCallerCanRead(t *testing.T) {
	freshRepoAccesses(t)
	jobs := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, jobs, "/archive/jobs/", "code1", 41)
	addJob(t, jobs, "/archive/jobs/", "code2", 41)
	addJob(t, jobs, "/jobs/", "site", 42)
	srv := &PatrickService{db: jobs}
	handler := srv.onlyReadableJobs(func(http.Context) (interface{}, error) {
		return project{ProjectId: "p", JobIds: []string{"code1", "site", "code2", "gone"}}, nil
	})

	client := &fakeRepos{perms: map[string]map[string]bool{"41": {"push": true}, "42": {"pull": true}}}
	got, err := handler(accessCtx("partial-token", client, nil))
	if err != nil {
		t.Fatal(err)
	}
	if ids := got.(project).JobIds; !slices.Equal(ids, []string{"code1", "code2"}) {
		t.Fatalf("listed %v", ids)
	}
	if client.asked != 2 {
		t.Fatalf("asked GitHub %d times for 2 repositories", client.asked)
	}

	// GitHub down: one question per repository, not one per job
	flaky := &fakeRepos{fail: &github.ErrorResponse{Response: &goHttp.Response{StatusCode: goHttp.StatusBadGateway}}}
	if _, err = handler(accessCtx("flaky-token", flaky, nil)); err != nil {
		t.Fatal(err)
	}
	if flaky.asked != 2 {
		t.Fatalf("asked a failing GitHub %d times for 2 repositories", flaky.asked)
	}
}

// movingJobs is a job on its way to the archive: absent from both places until
// the archive has been read once.
type movingJobs struct {
	*fakeJobs
	archiveReads int
}

func (d *movingJobs) Get(ctx context.Context, key string) ([]byte, error) {
	if key == "/archive/jobs/moving" {
		if d.archiveReads++; d.archiveReads == 1 {
			return nil, ds.ErrNotFound
		}
	}
	return d.fakeJobs.Get(ctx, key)
}

type brokenJobs struct{ *fakeJobs }

func (brokenJobs) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("store timed out")
}

func TestJobAccessOnlyLetsARealNotFoundThrough(t *testing.T) {
	stranger := &fakeRepos{perms: map[string]map[string]bool{"51": {"pull": true}}}
	ran := false
	run := func(srv *PatrickService, jid string) error {
		ran = false
		_, err := srv.withJobAccess("jid", func(http.Context) (interface{}, error) { ran = true; return "ok", nil })(
			accessCtx("lookup-"+jid, stranger, map[string]any{"jid": jid}))
		return err
	}

	jobs := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, jobs, "/archive/jobs/", "moving", 51)
	if err := run(&PatrickService{db: &movingJobs{fakeJobs: jobs}}, "moving"); !errors.Is(err, errNoJobAccess) || ran {
		t.Fatalf("a job moving to the archive went unchecked: err=%v ran=%v", err, ran)
	}

	if err := run(&PatrickService{db: brokenJobs{jobs}}, "moving"); err == nil || ran {
		t.Fatalf("a failed lookup reached the handler: err=%v ran=%v", err, ran)
	}

	if err := run(&PatrickService{db: jobs}, "nope"); err != nil || !ran {
		t.Fatalf("an unknown job should reach the handler: err=%v ran=%v", err, ran)
	}
}

// A finishing job is always in one place or the other, so a reader that looks
// in both cannot miss it.
type archiveOrderJobs struct {
	*fakeJobs
	t *testing.T
}

func (d *archiveOrderJobs) Put(_ context.Context, key string, v []byte) error {
	d.lock.Lock()
	defer d.lock.Unlock()
	d.keys[key] = v
	return nil
}

func (d *archiveOrderJobs) Delete(_ context.Context, key string) error {
	d.lock.Lock()
	defer d.lock.Unlock()
	if key == "/jobs/j" {
		if _, ok := d.keys["/archive/jobs/j"]; !ok {
			d.t.Error("job left /jobs/ before reaching the archive")
		}
	}
	delete(d.keys, key)
	return nil
}

func TestFinishedJobIsArchivedBeforeLeavingJobs(t *testing.T) {
	jobs := &fakeJobs{keys: map[string][]byte{}}
	addJob(t, jobs, "/jobs/", "j", 61)
	srv := &PatrickService{db: &archiveOrderJobs{fakeJobs: jobs, t: t}}
	if err := srv.updateStatus(context.Background(), "", "j", map[string]string{}, commonIface.JobStatusSuccess, map[string]string{}, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := jobs.keys["/jobs/j"]; ok {
		t.Fatal("job still in /jobs/")
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
	freshRepoAccesses(t)
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

// freshRepoAccesses gives a test its own access cache: the package-level one
// keeps answers across runs, so a repeated run would never ask GitHub.
func freshRepoAccesses(t *testing.T) {
	old := repoAccesses
	repoAccesses = &repoAccessCache{entries: make(map[[sha256.Size]byte]repoAccessEntry)}
	t.Cleanup(func() { repoAccesses = old })
}
