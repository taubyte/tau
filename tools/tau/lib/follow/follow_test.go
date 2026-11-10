package follow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	httpClient "github.com/taubyte/tau/clients/http"
	patrickHttp "github.com/taubyte/tau/clients/http/patrick"
	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

// fakePatrick serves /jobs/<project>, /job/<id> and /logs/<cid> like Patrick.
type fakePatrick struct {
	mu   sync.Mutex
	jobs map[string]*patrick.Job
	logs map[string]string
}

func (f *fakePatrick) set(j *patrick.Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[j.Id] = j
}

func (f *fakePatrick) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/jobs/"):
		ids := []string{}
		for id := range f.jobs {
			ids = append(ids, id)
		}
		json.NewEncoder(w).Encode(map[string]any{"ProjectId": "p", "JobIds": ids})
	case strings.HasPrefix(r.URL.Path, "/job/"):
		j, ok := f.jobs[strings.TrimPrefix(r.URL.Path, "/job/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Job": j})
	case strings.HasPrefix(r.URL.Path, "/logs/"):
		io.WriteString(w, f.logs[strings.TrimPrefix(r.URL.Path, "/logs/")])
	default:
		http.NotFound(w, r)
	}
}

func newPatrick(t *testing.T, jobs ...*patrick.Job) (*fakePatrick, Patrick) {
	f := &fakePatrick{jobs: map[string]*patrick.Job{}, logs: map[string]string{}}
	for _, j := range jobs {
		f.jobs[j.Id] = j
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := patrickHttp.New(context.Background(), httpClient.URL(srv.URL), httpClient.Auth("t"))
	assert.NilError(t, err)
	return f, c
}

func job(id, commit string, repo int, status patrick.JobStatus) *patrick.Job {
	return &patrick.Job{
		Id:        id,
		Timestamp: time.Now().Unix(),
		Status:    status,
		Attempt:   1,
		Meta: patrick.Meta{
			HeadCommit: patrick.HeadCommit{ID: commit},
			Repository: patrick.Repository{ID: repo, URI: fmt.Sprintf("git@github.com:me/tb_code_%d.git", repo)},
		},
	}
}

const poll = 10 * time.Millisecond

func TestWaitSuccess(t *testing.T) {
	_, p := newPatrick(t,
		job("old", "c0", 1, patrick.JobStatusFailed),
		job("j1", "c1", 1, patrick.JobStatusSuccess))

	jobs, code := Wait(context.Background(), p, "p", []Repo{{Name: "code", Commit: "c1"}}, poll)
	assert.Equal(t, code, 0)
	assert.Equal(t, len(jobs), 1)
	assert.Equal(t, jobs[0].Id, "j1")

	var out bytes.Buffer
	WriteJobs(&out, jobs)
	assert.Assert(t, strings.Contains(out.String(), "code"), out.String())
	assert.Assert(t, strings.Contains(out.String(), "success"), out.String())
	assert.Assert(t, strings.Contains(out.String(), "attempt 1"), out.String())
}

func TestWaitFollowsUntilFailed(t *testing.T) {
	f, p := newPatrick(t, job("j1", "c1", 1, patrick.JobStatusLocked))
	go func() {
		time.Sleep(50 * time.Millisecond)
		j := job("j1", "c1", 1, patrick.JobStatusFailed)
		j.Attempt = 2
		j.Logs = map[string]string{"res": "logcid"}
		f.mu.Lock()
		f.logs["logcid"] = "step 1\nstep 2\nerror: go build failed: undefined: Foo\n"
		f.mu.Unlock()
		f.set(j)
	}()

	jobs, code := Wait(context.Background(), p, "p", []Repo{{Name: "code", Commit: "c1"}}, poll)
	assert.Equal(t, code, 1)
	assert.Equal(t, jobs[0].Status, "failed")
	assert.Equal(t, jobs[0].Attempt, 2)
	assert.Equal(t, jobs[0].Error, "error: go build failed: undefined: Foo")

	var out bytes.Buffer
	WriteJobs(&out, jobs)
	assert.Assert(t, strings.Contains(out.String(), "undefined: Foo"), out.String())
	assert.Assert(t, strings.Contains(out.String(), "step 2"), out.String())
}

func TestWaitTimeout(t *testing.T) {
	_, p := newPatrick(t, job("j1", "c1", 1, patrick.JobStatusOpen))
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	jobs, code := Wait(ctx, p, "p", []Repo{{Name: "code", Commit: "c1"}, {Name: "config", Commit: "never"}}, poll)
	assert.Equal(t, code, 2)
	assert.Equal(t, jobs[0].Status, "open")
	assert.Equal(t, jobs[1].Status, "no job")
}

func TestServeReportsSlowNode(t *testing.T) {
	var flipped atomic.Bool
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, r.Host, "app.example.com")
		io.WriteString(w, "v2")
	}))
	defer fast.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if flipped.Load() {
			io.WriteString(w, "v2")
			return
		}
		io.WriteString(w, "v1")
	}))
	defer slow.Close()

	targets := []Target{
		{Resource: "fn", Node: "fast", URL: fast.URL + "/ping", Host: "app.example.com"},
		{Resource: "fn", Node: "slow", URL: slow.URL + "/ping", Host: "app.example.com"},
	}
	before := map[string]string{"fast": Hash(Probe(targets[0])), "slow": Hash(Probe(targets[1]))}
	before["fast"] = "v1-hash" // the fast node already moved on before we looked again
	time.AfterFunc(300*time.Millisecond, func() { flipped.Store(true) })

	served, code := Serve(context.Background(), targets, func(t Target) string { return before[t.Node] }, poll)
	assert.Equal(t, code, 0)
	assert.Equal(t, served[1].Node, "slow")
	assert.Assert(t, served[1].Changed)
	assert.Assert(t, served[1].Seconds >= 0.3, "%v", served[1].Seconds)
	assert.Assert(t, served[0].Seconds < served[1].Seconds)

	var out bytes.Buffer
	WriteServed(&out, served)
	assert.Assert(t, strings.Contains(out.String(), "slow"), out.String())
}

func TestServeTimeoutUnchanged(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "v1") }))
	defer node.Close()
	tg := Target{Resource: "fn", Node: "n1", URL: node.URL}
	old := Hash(Probe(tg))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	served, code := Serve(ctx, []Target{tg}, func(Target) string { return old }, poll)
	assert.Equal(t, code, 2)
	assert.Assert(t, !served[0].Changed)
}

func TestStatusRenderAndJSON(t *testing.T) {
	failed := job("j2", "c2", 7, patrick.JobStatusFailed)
	failed.Logs = map[string]string{"r": "l"}
	failed.Timestamp = time.Now().Unix() + 1 // newest for repo 7
	f, p := newPatrick(t,
		job("j1", "c1", 7, patrick.JobStatusSuccess),
		failed,
		job("w1", "c9", 9, patrick.JobStatusSuccess))
	f.mu.Lock()
	f.logs["l"] = "boom: exit status 1"
	f.mu.Unlock()

	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "v2") }))
	defer node.Close()

	rep, err := GetStatus(p, "p", map[int]string{9: "website site1"}, []Target{{Resource: "function fn", Node: "n1", URL: node.URL}})
	assert.NilError(t, err)
	assert.Equal(t, len(rep.Repos), 2)

	var out bytes.Buffer
	WriteStatus(&out, rep)
	s := out.String()
	assert.Assert(t, strings.Contains(s, "website site1"), s)
	assert.Assert(t, strings.Contains(s, "tb_code_7"), s)
	assert.Assert(t, strings.Contains(s, "boom: exit status 1"), s)
	assert.Assert(t, strings.Contains(s, "function fn"), s)

	b, err := json.Marshal(rep)
	assert.NilError(t, err)
	var shape struct {
		Repos []struct {
			Repo, Job, Status, Error string
			Attempt                  int
			AgeSeconds               float64 `json:"age_seconds"`
		} `json:"repos"`
		Serving []struct {
			Resource, Node, Hash string
			Code                 int
		} `json:"serving"`
	}
	assert.NilError(t, json.Unmarshal(b, &shape))
	assert.Equal(t, shape.Repos[0].Status, "failed")
	assert.Equal(t, shape.Repos[0].Error, "boom: exit status 1")
	assert.Equal(t, shape.Serving[0].Code, 200)
	assert.Assert(t, shape.Serving[0].Hash != "")
}
