package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/proto/gen/tau/auth/v2/authv2connect"
	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var mintedID = regexp.MustCompile(`Qm[1-9A-HJ-NP-Za-km-z]{44}`)

const goodToken = "tok-good"

// forgeToken is the fake forge's good token: the one a forge sign-in hands
// back to the CLI.
var forgeToken = fake.New(fake.GitHubLike).TokenString()

// takenID is a project id the fake frontend says somebody else claimed.
const takenID = "QmTakenTakenTakenTakenTakenTakenTakenTakenTake"

// frontend is a fake build frontend: the routes ingress.go mounts, answering
// from memory, refusing any token but goodToken the way the real one does.
type frontend struct {
	mu       sync.Mutex
	jobs     map[string]*buildv2.Job
	byProj   map[string][]string
	logs     map[string]string
	secrets  map[string]map[string][]byte
	created  []string
	grants   [][3]string
	fqdn     string
	calls    []string
	dropOnce bool
	// failSecrets makes every secret write a 503.
	failSecrets bool
	// pushAfter counts down list calls; at zero, pushed runs (under mu).
	pushAfter int
	pushed    func()
	auth      *fakeAuth
	srv       *httptest.Server

	// uploads makes this a development cloud that takes a build in.
	uploads bool
	// uploaded and queued are what an upload and a manual build were handed.
	uploaded []byte
	queued   []string
	// finish is the status a queued job ends in; published is what a
	// deployment's compiled object reads back as.
	finish      buildv2.JobStatus
	endOnFollow bool
	published   map[string][]byte
	// live is the jobs still being built, whose logs are still being written.
	live map[string]*liveLog
	// runLogs is a run's own log, keyed "job/attempt", where a test gives one;
	// gone is the runs whose log was not kept.
	runLogs map[string]string
	gone    map[string]bool
	// planFails is the reason a push's plan fails with; empty plans it.
	planFails string
	// rerunOnDrop makes the job a dropped follow was reading run again: the
	// run it was on loses its log and the next one starts. refused is the runs
	// whose read is a 503, which with rerunOnDrop run again too.
	rerunOnDrop bool
	refused     map[string]bool
	// unnamed leaves X-Job-Attempt off a log response.
	unnamed bool
	// logQueries is the query of every log read, in order.
	logQueries []string
}

func newFrontend(t *testing.T) *frontend {
	f := &frontend{
		finish:    buildv2.JobStatus_JobStatusSuccess,
		published: map[string][]byte{},
		jobs: map[string]*buildv2.Job{
			"job-1":    {Id: "job-1", Status: buildv2.JobStatus_JobStatusSuccess, RepositoryKey: "github.com/acme/pay", Ref: "refs/heads/main", CommitSha: "0123456789abcdef0123", Timestamp: 1700000000, Attempt: 1, Environment: "prod", AssetCid: map[string]string{"res-a": "bafyA"}, Logs: map[string]string{"1": "bafylog"}},
			"job-live": {Id: "job-live", Status: buildv2.JobStatus_JobStatusClaimed, RepositoryKey: "github.com/acme/pay", Ref: "refs/heads/main", CommitSha: "0123456789abcdef0123", Timestamp: 1700000050, Attempt: 1, Environment: "prod"},
			"job-2":    {Id: "job-2", Status: buildv2.JobStatus_JobStatusFailed, RepositoryKey: "github.com/acme/cfg", Ref: "refs/heads/main", CommitSha: "fedcba", Timestamp: 1700000100, Attempt: 2, Environment: "prod"},
		},
		byProj:  map[string][]string{"proj-1": {"job-1", "job-2"}},
		logs:    map[string]string{"job-1": "step one\nstep two\n", "job-live": "step one\nstep two\n"},
		runLogs: map[string]string{},
		gone:    map[string]bool{},
		refused: map[string]bool{},
		secrets: map[string]map[string][]byte{},
		fqdn:    "cloud.example",
	}
	f.auth = newFakeAuth(func() int64 { return time.Now().Unix() })
	mux := http.NewServeMux()
	mux.Handle(authv2connect.NewSessionServiceHandler(f.auth))
	mux.Handle(authv2connect.NewUserServiceHandler(f.auth))
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.calls = append(f.calls, r.Method+" "+r.URL.Path)
			f.mu.Unlock()
			if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); !ok || !f.auth.valid(tok) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
				return
			}
			h(w, r)
		}
	}
	denied := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}
	mux.HandleFunc("GET /jobs/{projectId}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		if f.pushAfter > 0 {
			// A push lands once the waiting CLI has looked this many times.
			if f.pushAfter--; f.pushAfter == 0 {
				f.pushed()
			}
		}
		ids, ok := f.byProj[r.PathValue("projectId")]
		f.mu.Unlock()
		if !ok {
			denied(w)
			return
		}
		json.NewEncoder(w).Encode(ids)
	}))
	mux.HandleFunc("GET /job/{jid}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		j, ok := f.jobs[r.PathValue("jid")]
		f.mu.Unlock()
		if !ok {
			denied(w)
			return
		}
		b, _ := protojson.MarshalOptions{UseProtoNames: true}.Marshal(j)
		w.Write(b)
	}))
	// THE LOG ROUTE SERVES ONE RUN, as the frontend's does: ?attempt=, or the
	// job's current run, named in X-Job-Attempt. An ended run's log is the one
	// it stored, and a run whose log was not kept is a 410.
	mux.HandleFunc("GET /job/{jid}/logs", authed(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("jid")
		f.mu.Lock()
		f.logQueries = append(f.logQueries, r.URL.RawQuery)
		live, j := f.live[id], f.jobs[id]
		var run int32
		var ended bool
		if j != nil {
			run, ended = currentRun(j), terminal(j)
		}
		f.mu.Unlock()
		if j == nil {
			denied(w)
			return
		}
		if asked, err := strconv.Atoi(r.URL.Query().Get("attempt")); err == nil {
			run = int32(asked)
		}
		attempt := strconv.Itoa(int(run))
		if live != nil && !live.over() {
			live.serve(w, r, attempt)
			return
		}
		f.mu.Lock()
		log, ok := f.runLogs[id+"/"+attempt]
		if !ok {
			log, ok = f.logs[id]
		}
		gone, refused, unnamed := f.gone[id+"/"+attempt], f.refused[id+"/"+attempt], f.unnamed
		from, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		drop := f.dropOnce && from == 0
		f.dropOnce = false
		if drop && f.rerunOnDrop {
			f.gone[id+"/"+attempt] = true
			j.Attempt = run + 1
		}
		if refused && f.rerunOnDrop {
			j.Attempt = run + 1
		}
		f.mu.Unlock()
		if refused {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "no backend available"})
			return
		}
		if gone {
			w.Header().Set("X-Job-Attempt", attempt)
			w.WriteHeader(http.StatusGone)
			json.NewEncoder(w).Encode(map[string]string{"error": "this run's log was not kept"})
			return
		}
		if !ok {
			denied(w)
			return
		}
		if ended {
			log = "stored: " + log
		}
		rest := log[min(from, len(log)):]
		if drop {
			// Send a little, then cut the connection mid-stream.
			hj, _ := w.(http.Hijacker)
			conn, buf, _ := hj.Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nX-Job-Attempt: " + attempt + "\r\nTransfer-Encoding: chunked\r\n\r\n")
			buf.WriteString(strconv.FormatInt(int64(len(rest[:5])), 16) + "\r\n" + rest[:5] + "\r\n")
			buf.Flush()
			conn.Close()
			return
		}
		if !unnamed {
			w.Header().Set("X-Job-Attempt", attempt)
		}
		io.WriteString(w, rest)
		// A FOLLOWED BUILD ENDS when its log does, as the backend's follow
		// returns when the build finishes.
		f.mu.Lock()
		if j := f.jobs[r.PathValue("jid")]; j != nil && f.endOnFollow && j.Status == buildv2.JobStatus_JobStatusClaimed {
			j.Status = buildv2.JobStatus_JobStatusSuccess
		}
		f.mu.Unlock()
	}))
	mux.HandleFunc("POST /cancel/{jid}", authed(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	mux.HandleFunc("POST /retry/{jid}", authed(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("jid") == "busy" {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "busy"})
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	secretKey := func(r *http.Request) string { return r.PathValue("projectId") + "/" + r.PathValue("environment") }
	mux.HandleFunc("GET /secrets/{projectId}/{environment}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		names := []string{}
		for n := range f.secrets[secretKey(r)] {
			names = append(names, n)
		}
		json.NewEncoder(w).Encode(names)
	}))
	mux.HandleFunc("PUT /secrets/{projectId}/{environment}/{name}", authed(func(w http.ResponseWriter, r *http.Request) {
		v, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failSecrets {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.secrets[secretKey(r)] == nil {
			f.secrets[secretKey(r)] = map[string][]byte{}
		}
		f.secrets[secretKey(r)][r.PathValue("name")] = v
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("DELETE /secrets/{projectId}/{environment}/{name}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.secrets[secretKey(r)], r.PathValue("name"))
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /projects/{projectId}", authed(func(w http.ResponseWriter, r *http.Request) {
		type hookBody struct {
			Config string   `json:"config_repository_key"`
			Code   []string `json:"code_repository_keys"`
		}
		var body hookBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		id := r.PathValue("projectId")
		if id == takenID {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "register: project id is already claimed: project " + id + " is already claimed by github.com/someone/else; generate a new id in project.yaml"})
			return
		}
		f.mu.Lock()
		f.created = append(f.created, id+"|"+body.Config+"|"+strings.Join(body.Code, ","))
		f.byProj[id] = nil
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": id, "config_repository_key": body.Config, "code_repository_keys": body.Code})
	}))
	mux.HandleFunc("GET /projects", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		out := []map[string]any{}
		for _, c := range f.created {
			parts := strings.SplitN(c, "|", 3)
			out = append(out, map[string]any{"id": parts[0], "config_repository_key": parts[1], "code_repository_keys": strings.Split(parts[2], ",")})
		}
		json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("GET /cloud", authed(func(w http.ResponseWriter, r *http.Request) {
		if f.fqdn == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "this build frontend was given no cloud fqdn"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"fqdn": f.fqdn, "uploads": f.uploads})
	}))
	queue := func(w http.ResponseWriter, what string) {
		f.mu.Lock()
		// A PUSH IS A PLAN, which queues one job per environment the commit feeds:
		// here one, which is where the build and its log are.
		id := "job-" + strconv.Itoa(len(f.queued)+3)
		built := id + "-prod"
		f.queued = append(f.queued, what)
		f.jobs[id] = &buildv2.Job{Id: id, Status: buildv2.JobStatus_JobStatusSuccess, Timestamp: 1700000300, Attempt: 1, Children: []string{built}}
		if f.planFails != "" {
			f.jobs[id].Status, f.jobs[id].Children, f.jobs[id].Reason = buildv2.JobStatus_JobStatusFailed, nil, f.planFails
		}
		f.jobs[built] = &buildv2.Job{Id: built, Status: f.finish, Timestamp: 1700000300, Attempt: 1, Environment: "prod", Logs: map[string]string{"1": "bafylog"}}
		f.logs[built] = "built " + what + "\n"
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"job_id": id})
	}
	mux.HandleFunc("POST /build/{projectId}", authed(func(w http.ResponseWriter, r *http.Request) {
		type pushBody struct {
			Repository string `json:"repository_key"`
			Ref        string `json:"ref"`
			Commit     string `json:"commit_sha"`
		}
		var body pushBody
		json.NewDecoder(r.Body).Decode(&body)
		if body.Ref == "refs/heads/feature" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			json.NewEncoder(w).Encode(map[string]string{"error": "refs/heads/feature is not a ref this cloud builds"})
			return
		}
		queue(w, r.PathValue("projectId")+" "+body.Repository+" "+body.Ref+" "+body.Commit)
	}))
	mux.HandleFunc("POST /upload/{projectId}", authed(func(w http.ResponseWriter, r *http.Request) {
		if !f.uploads {
			denied(w)
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.uploaded = body
		f.mu.Unlock()
		what := r.PathValue("projectId") + " upload " + r.URL.Query().Get("ref")
		if repo := r.URL.Query().Get("repository"); repo != "" {
			what += " " + repo
		}
		queue(w, what)
	}))
	mux.HandleFunc("GET /job/{jid}/logs/{key}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		log, ok := f.runLogs[r.PathValue("jid")+"/"+strings.TrimLeft(r.PathValue("key"), "0")]
		if !ok {
			log, ok = f.logs[r.PathValue("jid")]
		}
		f.mu.Unlock()
		if !ok {
			denied(w)
			return
		}
		io.WriteString(w, "stored: "+log)
	}))
	mux.HandleFunc("GET /projects/{projectId}/deployments", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		envs := []string{}
		for key := range f.published {
			if env, ok := strings.CutPrefix(key, r.PathValue("projectId")+"/"); ok {
				envs = append(envs, env)
			}
		}
		if len(envs) == 0 {
			denied(w)
			return
		}
		sort.Strings(envs)
		json.NewEncoder(w).Encode(envs)
	}))
	mux.HandleFunc("GET /projects/{projectId}/deployments/{environment}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		compiled, ok := f.published[r.PathValue("projectId")+"/"+r.PathValue("environment")]
		f.mu.Unlock()
		if !ok {
			denied(w)
			return
		}
		w.Write(compiled)
	}))
	mux.HandleFunc("POST /domain/{fqdn}/for/{projectId}", authed(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("projectId") != "proj-1" {
			denied(w)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"entry": "proj-1xx." + r.PathValue("fqdn"), "type": "txt", "token": "signed"})
	}))
	mux.HandleFunc("GET /projects/{projectId}/grants", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.PathValue("projectId") != "proj-1" {
			denied(w)
			return
		}
		out := []map[string]any{}
		for _, g := range f.grants {
			out = append(out, map[string]any{"id": g[0], "user": g[1], "role": g[2], "by": "u-dev", "at": "2026-10-21T00:00:00Z"})
		}
		json.NewEncoder(w).Encode(out)
	}))
	mux.HandleFunc("POST /projects/{projectId}/grants", authed(func(w http.ResponseWriter, r *http.Request) {
		type grantBody struct{ User, Role string }
		var body grantBody
		json.NewDecoder(r.Body).Decode(&body)
		if body.Role == "emperor" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "not a role: emperor"})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		id := "g-" + strconv.Itoa(len(f.grants)+1)
		f.grants = append(f.grants, [3]string{id, body.User, body.Role})
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"id": id})
	}))
	mux.HandleFunc("DELETE /projects/{projectId}/grants/{grantId}", authed(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, g := range f.grants {
			if g[0] == r.PathValue("grantId") {
				f.grants = append(f.grants[:i], f.grants[i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		denied(w)
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// addJob makes a job appear, as a push would.
func (f *frontend) addJob(project, id, log string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addJobLocked(project, id, log)
}

// pushLater makes a job appear on the second list after this — after a
// waiting deploy has taken its snapshot of what already existed.
func (f *frontend) pushLater(project, id, log string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushAfter = 2
	f.pushed = func() { f.addJobLocked(project, id, log) }
}

func (f *frontend) addJobLocked(project, id, log string) {
	f.jobs[id] = &buildv2.Job{Id: id, Status: buildv2.JobStatus_JobStatusClaimed, Timestamp: 1700000200, Attempt: 1, Environment: "prod"}
	f.byProj[project] = append(f.byProj[project], id)
	f.logs[id] = log
}

// liveLog is a running job's log the way the build frontend serves it: a
// follow is held open and sent each line as it is written until the job ends,
// and a snapshot is what is there now.
type liveLog struct {
	f     *frontend
	id    string
	mu    sync.Mutex
	text  string
	grew  chan struct{}
	ended bool
}

// building puts a running job on the fake whose log so far is first.
func (f *frontend) building(t *testing.T, id, first string) *liveLog {
	l := &liveLog{f: f, id: id, text: first, grew: make(chan struct{})}
	f.mu.Lock()
	f.jobs[id] = &buildv2.Job{Id: id, Status: buildv2.JobStatus_JobStatusClaimed, Timestamp: 1700000400, Attempt: 1, Environment: "prod"}
	if f.live == nil {
		f.live = map[string]*liveLog{}
	}
	f.live[id] = l
	f.mu.Unlock()
	// After newFrontend's, so the job ends before the server waits on a follow.
	t.Cleanup(l.end)
	return l
}

// queuedJob puts a job on the fake that no node has started: a follow of it is
// held open until it does, and a snapshot of it is empty.
func (f *frontend) queuedJob(t *testing.T, id string) *liveLog {
	l := f.building(t, id, "")
	f.mu.Lock()
	f.jobs[id].Status, f.jobs[id].Attempt = buildv2.JobStatus_JobStatusOpen, 0
	f.mu.Unlock()
	return l
}

// start is a queued job being claimed and printing its first line.
func (l *liveLog) start(first string) {
	l.f.mu.Lock()
	l.f.jobs[l.id].Status, l.f.jobs[l.id].Attempt = buildv2.JobStatus_JobStatusClaimed, 1
	l.f.mu.Unlock()
	l.say(first)
}

// currentRun is the run a log read is about when it names none, as the
// frontend decides it: the one claimed or last run, or a queued job's next.
func currentRun(j *buildv2.Job) int32 {
	if j.GetStatus() == buildv2.JobStatus_JobStatusOpen || j.GetAttempt() == 0 {
		return j.GetAttempt() + 1
	}
	return j.GetAttempt()
}

// over is whether the job has ended, after which its log is the stored one.
func (l *liveLog) over() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ended
}

func (l *liveLog) say(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.text += line
	close(l.grew)
	l.grew = make(chan struct{})
}

// end is the job finishing: a follow of it ends, and its log is the stored one.
// The record and the stored log are written before the log ends, so a read
// that finds it over finds them.
func (l *liveLog) end() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended {
		return
	}
	l.f.mu.Lock()
	l.f.jobs[l.id].Status = buildv2.JobStatus_JobStatusSuccess
	l.f.jobs[l.id].Logs = map[string]string{"1": "bafylog"}
	l.f.logs[l.id] = l.text
	l.f.mu.Unlock()
	l.ended = true
	close(l.grew)
}

func (l *liveLog) serve(w http.ResponseWriter, r *http.Request, attempt string) {
	from, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	snapshot := r.URL.Query().Get("snapshot") == "true"
	// The response opens before the first chunk, as the frontend's does.
	w.Header().Set("X-Job-Attempt", attempt)
	w.(http.Flusher).Flush()
	for {
		l.mu.Lock()
		text, grew, ended := l.text, l.grew, l.ended
		l.mu.Unlock()
		if from < len(text) {
			io.WriteString(w, text[from:])
			w.(http.Flusher).Flush()
			from = len(text)
		}
		if snapshot || ended {
			return
		}
		select {
		case <-grew:
		case <-r.Context().Done():
			return
		}
	}
}

// prompter answers from a script, in order, and records what it was asked.
type prompter struct {
	answers []string
	asked   []string
	err     error
}

func (p *prompter) next(title string) (string, error) {
	p.asked = append(p.asked, title)
	if p.err != nil {
		return "", p.err
	}
	if len(p.answers) == 0 {
		return "", errors.New("prompter: no answer scripted for " + title)
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a, nil
}

func (p *prompter) Input(title, _ string, validate func(string) error) (string, error) {
	a, err := p.next(title)
	if err == nil && validate != nil {
		if verr := validate(a); verr != nil {
			return "", verr
		}
	}
	return a, err
}
func (p *prompter) Secret(title, _ string) (string, error) { return p.next(title) }
func (p *prompter) Confirm(title string) (bool, error) {
	a, err := p.next(title)
	return a == "yes", err
}
func (p *prompter) Select(title string, options []string) (string, error) {
	a, err := p.next(title)
	if err == nil && !contains(options, a) {
		return "", errors.New("not an option: " + a)
	}
	return a, err
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}

// drive runs a bubbletea model the way a Program does — Init, then each
// command's message back into Update — without a terminal. Spinner ticks are
// dropped (they only animate), so the loop ends when the model quits or runs
// out of work.
func drive(m tea.Model, _ bool) (tea.Model, error) {
	queue := []tea.Cmd{m.Init(), func() tea.Msg { return tea.WindowSizeMsg{Width: 80, Height: 24} }}
	for steps := 0; len(queue) > 0 && steps < 1000; steps++ {
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		switch msg := msg.(type) {
		case tea.QuitMsg:
			return m, nil
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case spinner.TickMsg:
			continue
		}
		var next tea.Cmd
		m, next = m.Update(msg)
		queue = append(queue, next)
	}
	return m, nil
}

type run struct {
	code   int
	stdout string
	stderr string
}

type tester struct {
	t           *testing.T
	forges      *forge.Registry
	f           *frontend
	dir         string
	interactive bool
	prompter    *prompter
	stdin       string
	browse      func(string) error
	engine      artifact.Engine
}

func newTester(t *testing.T) *tester {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "tau", "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")
	t.Setenv("TAU_PROFILE", "")
	t.Setenv("TAU_PROJECT", "")
	return &tester{t: t, f: newFrontend(t), dir: t.TempDir(), prompter: &prompter{}}
}

func (x *tester) env(stdout, stderr io.Writer) Env {
	return Env{
		Stdin: strings.NewReader(x.stdin), Stdout: stdout, Stderr: stderr,
		Interactive: x.interactive, Prompter: x.prompter, HTTP: x.f.srv.Client(), Program: drive,
		Forges: x.forges, Browse: x.browse, Engine: x.engine,
	}
}

func (x *tester) run(args ...string) run {
	x.t.Helper()
	var out, errs bytes.Buffer
	code := Run(x.env(&out, &errs), args)
	return run{code: code, stdout: out.String(), stderr: errs.String()}
}

// start runs the CLI in the background with stdout going to out, and hands
// back its exit code once it has one.
func (x *tester) start(out io.Writer, args ...string) <-chan int {
	return x.startWith(out, io.Discard, args...)
}

// startWith is start with stderr going to errs.
func (x *tester) startWith(out, errs io.Writer, args ...string) <-chan int {
	exited := make(chan int, 1)
	env := x.env(out, errs)
	go func() { exited <- Run(env, args) }()
	return exited
}

// syncBuffer is a stdout a test reads while the CLI is still writing it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (x *tester) ok(args ...string) string {
	x.t.Helper()
	r := x.run(args...)
	require.Equal(x.t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	return r.stdout
}

func (x *tester) login() {
	x.t.Helper()
	x.ok("login", "--cloud", x.f.srv.URL, "--token", goodToken)
}

func (x *tester) link(project string) {
	x.t.Helper()
	require.NoError(x.t, config.WriteLink(x.dir, config.Link{ProjectID: project, Name: "shop", Config: "github.com/acme/cfg", Code: []string{"github.com/acme/pay"}}))
}

// golden compares got against testdata/name.golden, rewriting it under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	// Minted ids differ per project; the shape around them is what is pinned.
	got = mintedID.ReplaceAllString(got, "<id>")
	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run with -update to create it")
	require.Equal(t, string(want), got)
}
