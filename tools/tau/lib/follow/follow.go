// Package follow watches what a push did: the builds Patrick ran for it and
// what each node serves afterwards. It only reads Patrick's jobs API and the
// nodes' HTTP endpoints; it never calls auth (its /projects costs GitHub calls).
package follow

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pterm/pterm"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/tools/tau/output"
)

// Patrick is the part of the Patrick client this package reads.
type Patrick interface {
	JobsLast(projectId string, last int) ([]string, error)
	Job(jid string) (*patrick.Job, error)
	LogFile(jobId, resourceId string) (io.ReadCloser, error)
}

// Repo is a pushed repository and the commit its build must be for.
// Remote (the GitHub repo name) and Since (push time, unix) match a job that
// carries no commit or another one, as Dream's injected jobs do.
type Repo struct {
	Name   string
	Commit string
	Remote string
	Since  int64
	Branch string
}

// Job is one build as the user sees it.
type Job struct {
	Repo    string   `json:"repo"`
	Id      string   `json:"job"`
	Status  string   `json:"status"`
	Attempt int      `json:"attempt"`
	Seconds float64  `json:"seconds"`
	Error   string   `json:"error,omitempty"`
	Logs    []string `json:"logs,omitempty"`
}

const recentJobs = 50

func finished(s patrick.JobStatus) bool {
	return s == patrick.JobStatusSuccess || s == patrick.JobStatusFailed || s == patrick.JobStatusCancelled
}

// fetch reads the project's recent jobs; finished jobs never change, so they
// are read once and kept in cache.
func fetch(p Patrick, project string, last int, cache map[string]*patrick.Job) ([]*patrick.Job, error) {
	ids, err := p.JobsLast(project, last)
	if err != nil {
		return nil, err
	}
	jobs := make([]*patrick.Job, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, id := range ids {
		if j, ok := cache[id]; ok {
			jobs[i] = j
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			if j, err := p.Job(id); err == nil {
				j.Id = id
				jobs[i] = j
			}
		}()
	}
	wg.Wait()
	out := jobs[:0]
	for _, j := range jobs {
		if j != nil {
			if finished(j.Status) {
				cache[j.Id] = j
			}
			out = append(out, j)
		}
	}
	return out, nil
}

// match picks the newest job of the pushed commit, else the newest job of the
// same repository created since the push (Dream's injected jobs carry a
// placeholder commit). The commit match comes first: it is right under clock skew.
func match(jobs []*patrick.Job, r Repo) *patrick.Job {
	var exact, near *patrick.Job
	for _, j := range jobs {
		switch {
		case r.Commit != "" && j.Meta.HeadCommit.ID == r.Commit:
			if exact == nil || j.Timestamp > exact.Timestamp {
				exact = j
			}
		case r.Remote != "" && j.Timestamp >= r.Since && repoName(j.Meta.Repository) == r.Remote:
			if near == nil || j.Timestamp > near.Timestamp {
				near = j
			}
		}
	}
	if exact != nil {
		return exact
	}
	return near
}

// progressLine says what changed in one repo's job, or "" when nothing did.
func progressLine(prev, cur Job) string {
	switch {
	case cur.Id == "" || (prev.Id == cur.Id && prev.Status == cur.Status && prev.Attempt == cur.Attempt):
		return ""
	case cur.Status == "open":
		return fmt.Sprintf("%-8s queued    job %s", cur.Repo, cur.Id)
	case cur.Status == "locked":
		return fmt.Sprintf("%-8s building attempt %d  job %s", cur.Repo, cur.Attempt, cur.Id)
	default: // finished: Wait writes it with WriteJobs, the job's one final line
		return "final"
	}
}

// heartbeat: Patrick may keep a job "open" until it ends (t3 said nothing for
// 4m45s), so a running job gets a line this often when nothing changed.
var heartbeat = time.Minute

// serveQuiet: a build serves within seconds of its job ending, so a push whose
// targets all stay unchanged this long changed nothing served.
var serveQuiet = time.Minute

// ExitQuiet is Serve's exit code when nothing served changed within serveQuiet:
// not a success, because a rollout that never propagates looks the same.
const ExitQuiet = 3

// Wait follows each pushed repo's job (see match) until all end, writing a
// line to progress (if not nil) as a job appears, starts, retries or ends,
// and every heartbeat while a job runs with no change.
// Exit code: 0 all succeeded, 1 one failed or was canceled, 2 ctx ended first.
func Wait(ctx context.Context, p Patrick, project string, repos []Repo, poll time.Duration, progress io.Writer) ([]Job, int) {
	cache := map[string]*patrick.Job{}
	out := make([]Job, len(repos))
	said := make([]time.Time, len(repos))
	for i, r := range repos {
		said[i] = time.Now()
		out[i] = Job{Repo: r.Name, Status: "no job"}
	}
	for {
		jobs, _ := fetch(p, project, recentJobs, cache) // a missed poll is retried
		done := 0
		for i, r := range repos {
			newest := match(jobs, r)
			if newest == nil {
				continue
			}
			prev := out[i]
			// Seconds: from the push (else the job's creation) to when we saw it end
			from := time.Unix(newest.Timestamp, 0)
			if r.Since > 0 {
				from = time.Unix(r.Since, 0)
			}
			out[i] = Job{Repo: r.Name, Id: newest.Id, Status: status(newest.Status), Attempt: newest.Attempt,
				Seconds: time.Since(from).Round(time.Second).Seconds()}
			if prev.Id == newest.Id && prev.Status == out[i].Status && finished(newest.Status) {
				out[i].Seconds = prev.Seconds
			}
			if finished(newest.Status) {
				done++
				if newest.Status != patrick.JobStatusSuccess {
					if prev.Id == newest.Id && prev.Error != "" {
						out[i].Error, out[i].Logs = prev.Error, prev.Logs
					} else {
						out[i].Error, out[i].Logs = logTail(p, newest)
					}
				}
			}
			switch l := progressLine(prev, out[i]); {
			case progress == nil:
			case l == "final":
				WriteJobs(progress, out[i:i+1])
			case l != "":
				fmt.Fprintln(progress, l)
				said[i] = time.Now()
			case !finished(newest.Status) && time.Since(said[i]) >= heartbeat:
				fmt.Fprintf(progress, "%-8s still building, %s after push  job %s\n", r.Name, time.Since(from).Round(time.Second), newest.Id)
				said[i] = time.Now()
			}
		}
		if done == len(repos) {
			return out, exitCode(out, false)
		}
		select {
		case <-ctx.Done():
			return out, exitCode(out, true)
		case <-time.After(poll):
		}
	}
}

func exitCode(jobs []Job, timedOut bool) int {
	for _, j := range jobs {
		if j.Status == "failed" || j.Status == "canceled" {
			return 1
		}
	}
	if timedOut {
		return 2
	}
	return 0
}

func status(s patrick.JobStatus) string { return strings.ToLower(s.String()) }

const tailLines = 10

// logTail returns the job's last log lines and, as its error, the last line
// that says "error" (else the last line): Patrick keeps no error field.
func logTail(p Patrick, j *patrick.Job) (string, []string) {
	var lines []string
	for _, cid := range j.Logs {
		rc, err := p.LogFile(j.Id, cid)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		rc.Close()
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				lines = append(lines, l)
			}
		}
	}
	if len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	if j.Reason != "" {
		return j.Reason, lines
	}
	if len(lines) == 0 {
		return "no logs", nil
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(lines[i]), "error") {
			return lines[i], lines
		}
	}
	return lines[len(lines)-1], lines
}

// WriteJobs prints one line per job, and the error and log tail of a failure.
func WriteJobs(w io.Writer, jobs []Job) {
	for _, j := range jobs {
		fmt.Fprintf(w, "%s %-8s %-8s attempt %d  %4.0fs after push  %s\n", output.JobMark(j.Status), j.Repo, j.Status, j.Attempt, j.Seconds, j.Id)
		if j.Error != "" {
			fmt.Fprintf(w, "  error: %s\n", j.Error)
			for _, l := range j.Logs {
				fmt.Fprintf(w, "  | %s\n", l)
			}
		}
	}
}

// Target is one node's URL for one website or function.
type Target struct {
	Resource string // "function fn1"
	Node     string
	URL      string // full URL on that node
	Host     string // Host header (and TLS name) the node routes on
	Method   string // the function's first method; "" is GET
}

// Probe calls the target with its method; code 0 means it did not answer.
func Probe(t Target) (int, []byte) {
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, t.URL, nil)
	if err != nil {
		return 0, nil
	}
	tr := &http.Transport{}
	if t.Host != "" {
		req.Host = t.Host
		tr.TLSClientConfig = &tls.Config{ServerName: t.Host}
	}
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, b
}

// Hash names what a node serves: status code and a body digest.
func Hash(code int, body []byte) string {
	if code == 0 {
		return ""
	}
	return fmt.Sprintf("%d:%x", code, sha256.Sum256(body))[:16]
}

// Served is what one node serves for one resource.
type Served struct {
	Resource string  `json:"resource"`
	Node     string  `json:"node"`
	Code     int     `json:"code"`
	Hash     string  `json:"hash"`
	Changed  bool    `json:"changed"`
	Seconds  float64 `json:"seconds"`
	Note     string  `json:"note,omitempty"`
}

// Baseline probes each target before the push. A target that answers not at
// all, or a function that answers 404, cannot show a new build: it is left out
// and its resource named once. A website's 404 (a new site) is a fine baseline.
func Baseline(targets []Target) (keep []Target, before map[Target]string, skipped []string) {
	before = map[Target]string{}
	seen := map[string]bool{}
	for _, t := range targets {
		code, body := Probe(t)
		if code == 0 || code == http.StatusNotFound && !strings.HasPrefix(t.Resource, "website ") {
			if !seen[t.Resource] {
				seen[t.Resource] = true
				skipped = append(skipped, fmt.Sprintf("%s: can't fingerprint it (HTTP %d on %s %s), not waited on", t.Resource, code, t.Node, t.URL))
			}
			continue
		}
		keep, before[t] = append(keep, t), Hash(code, body)
	}
	return keep, before, skipped
}

// Serve polls the targets until a resource the push changed serves the new
// build on every node, and every resource seen changing does so on all its
// nodes. The push may not touch every resource, so the ones still unchanged
// then are not waited on: they are "not changed by this push".
// shortcut: a resource that changes later than the others is cut off; upgrade
// by matching the build's asset CIDs per function once Patrick exposes them.
// When no resource has changed serveQuiet after Serve began, either the push
// changed nothing served or the nodes never picked it up; Serve cannot tell
// which, so it says so on progress and returns ExitQuiet, not 0. While waiting
// it writes a line to progress (if not nil) every heartbeat.
// Exit code 0, ExitQuiet, or 2 if ctx ended first.
func Serve(ctx context.Context, targets []Target, before func(Target) string, poll time.Duration, start time.Time, progress io.Writer) ([]Served, int) {
	entered, said := time.Now(), time.Now()
	out := make([]Served, len(targets))
	old := make([]string, len(targets))
	for i, t := range targets {
		out[i], old[i] = Served{Resource: t.Resource, Node: t.Node}, before(t)
	}
	for {
		var wg sync.WaitGroup
		for i, t := range targets {
			if out[i].Changed {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				code, body := Probe(t)
				out[i].Code, out[i].Hash = code, Hash(code, body)
				if code != 0 && out[i].Hash != old[i] {
					out[i].Changed = true
					out[i].Seconds = time.Since(start).Round(100 * time.Millisecond).Seconds()
				}
			}()
		}
		wg.Wait()

		all, some := map[string]bool{}, map[string]bool{}
		for _, s := range out {
			if _, ok := all[s.Resource]; !ok {
				all[s.Resource] = true
			}
			all[s.Resource] = all[s.Resource] && s.Changed
			some[s.Resource] = some[s.Resource] || s.Changed
		}
		settled, someDone, anyChanged := true, false, false
		for r := range all {
			settled = settled && all[r] == some[r]
			someDone = someDone || all[r]
			anyChanged = anyChanged || some[r]
		}
		quiet := !anyChanged && time.Since(entered) >= serveQuiet
		if quiet && progress != nil {
			fmt.Fprintf(progress, "nothing serves a new build %s after the builds ended: either this push changed nothing served or the nodes have not picked it up (exit %d)\n", serveQuiet, ExitQuiet)
		}
		if settled && someDone || quiet {
			for i := range out {
				if !out[i].Changed {
					out[i].Note = "not changed by this push"
				}
			}
			if quiet {
				return out, ExitQuiet
			}
			return out, 0
		}
		if progress != nil && time.Since(said) >= heartbeat {
			waiting := 0
			for _, s := range out {
				if !s.Changed {
					waiting++
				}
			}
			fmt.Fprintf(progress, "still waiting for %d of %d nodes to serve the new build, %s after push\n", waiting, len(out), time.Since(start).Round(time.Second))
			said = time.Now()
		}
		select {
		case <-ctx.Done():
			return out, 2
		case <-time.After(poll):
		}
	}
}

// WriteServed prints per node how long it took to serve the new build.
func WriteServed(w io.Writer, served []Served) {
	for _, s := range served {
		switch {
		case s.Changed:
			fmt.Fprintf(w, "%s %-20s %-24s serves new build %.1fs after push (HTTP %d)\n", output.OK(), s.Resource, s.Node, s.Seconds, s.Code)
		case s.Note != "":
			fmt.Fprintf(w, "%s %-20s %-24s %s (HTTP %d)\n", output.Warn(), s.Resource, s.Node, s.Note, s.Code)
		default:
			fmt.Fprintf(w, "%s %-20s %-24s unchanged (HTTP %d)\n", output.Failed(), s.Resource, s.Node, s.Code)
		}
	}
}

// RepoStatus is the last build of one repository.
type RepoStatus struct {
	Repo       string  `json:"repo"`
	Job        string  `json:"job"`
	Status     string  `json:"status"`
	Attempt    int     `json:"attempt"`
	AgeSeconds float64 `json:"age_seconds"`
	Error      string  `json:"error,omitempty"`
}

// Report is `tau status`.
type Report struct {
	Repos   []RepoStatus `json:"repos"`
	Serving []Served     `json:"serving"`
}

// GetStatus reads the last build per repository and what each node serves.
// names labels repository ids (websites, libraries); others use the repo name.
func GetStatus(p Patrick, project string, names map[int]string, targets []Target) (Report, error) {
	jobs, err := fetch(p, project, recentJobs, map[string]*patrick.Job{})
	if err != nil {
		return Report{}, err
	}
	newest := map[int]*patrick.Job{}
	for _, j := range jobs {
		id := j.Meta.Repository.ID
		if n := newest[id]; n == nil || j.Timestamp > n.Timestamp {
			newest[id] = j
		}
	}
	rep := Report{Repos: []RepoStatus{}, Serving: []Served{}}
	for id, j := range newest {
		name := names[id]
		if name == "" {
			name = repoLabel(j.Meta.Repository)
		}
		r := RepoStatus{Repo: name, Job: j.Id, Status: status(j.Status), Attempt: j.Attempt,
			AgeSeconds: time.Since(time.Unix(j.Timestamp, 0)).Round(time.Second).Seconds()}
		if j.Status == patrick.JobStatusFailed {
			r.Error, _ = logTail(p, j)
		}
		rep.Repos = append(rep.Repos, r)
	}
	// the serve table's websites and the project's repos all get a row
	have := map[string]bool{}
	for _, r := range rep.Repos {
		have[r.Repo] = true
	}
	want := []string{}
	for _, n := range names {
		want = append(want, n)
	}
	for _, t := range targets {
		if strings.HasPrefix(t.Resource, "website ") {
			want = append(want, t.Resource)
		}
	}
	for _, n := range want {
		if !have[n] {
			have[n] = true
			rep.Repos = append(rep.Repos, RepoStatus{Repo: n, Status: "no build yet"})
		}
	}
	sort.Slice(rep.Repos, func(a, b int) bool { return rep.Repos[a].Repo < rep.Repos[b].Repo })

	rep.Serving = make([]Served, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := Probe(t)
			rep.Serving[i] = Served{Resource: t.Resource, Node: t.Node, Code: code, Hash: Hash(code, body)}
		}()
	}
	wg.Wait()
	return rep, nil
}

func repoName(r patrick.Repository) string {
	u := r.URI
	if u == "" {
		u = r.SSHURL
	}
	if u == "" {
		return fmt.Sprintf("repo %d", r.ID)
	}
	return RepoName(u)
}

// repoLabel names a repo as tau does: config, code, website x, library x.
func repoLabel(r patrick.Repository) string {
	n := repoName(r)
	switch {
	case strings.HasPrefix(n, "tb_code_"):
		return "code"
	case strings.HasPrefix(n, "tb_website_"):
		return "website " + strings.TrimPrefix(n, "tb_website_")
	case strings.HasPrefix(n, "tb_library_"):
		return "library " + strings.TrimPrefix(n, "tb_library_")
	case strings.HasPrefix(n, "tb_"):
		return "config"
	}
	return n
}

// RepoName is the repository name in a git URL or path.
func RepoName(u string) string {
	u = strings.TrimRight(u, "/")
	return strings.TrimSuffix(u[strings.LastIndexAny(u, ":/")+1:], ".git")
}

// WriteStatus prints the report as two tables. A node whose answer differs
// from the other nodes of the same resource is marked: it is behind or ahead.
func WriteStatus(w io.Writer, rep Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tSTATUS\tATTEMPT\tAGE\tJOB")
	for _, r := range rep.Repos {
		// no color in a table: tabwriter would count the escapes as width
		fmt.Fprintf(tw, "%s\t%s %s\t%d\t%s\t%s\n", r.Repo, pterm.RemoveColorFromString(output.JobMark(r.Status)), r.Status, r.Attempt, time.Duration(r.AgeSeconds)*time.Second, r.Job)
		if r.Error != "" {
			fmt.Fprintf(tw, "  error: %s\t\t\t\t\n", r.Error)
		}
	}
	if len(rep.Serving) > 0 {
		// count what the answering nodes of each resource serve
		count := map[string]map[string]int{}
		for _, s := range rep.Serving {
			if count[s.Resource] == nil {
				count[s.Resource] = map[string]int{}
			}
			if s.Code != 0 {
				count[s.Resource][s.Hash]++
			}
		}
		tw.Flush()
		fmt.Fprintln(w)
		fmt.Fprintln(tw, "RESOURCE\tNODE\tHTTP\tSERVES\t")
		for _, s := range rep.Serving {
			answering, most := 0, 0
			for _, n := range count[s.Resource] {
				answering += n
				most = max(most, n)
			}
			note := ""
			switch {
			case s.Code == 0:
				note = "no answer"
			case count[s.Resource][s.Hash] < most || len(count[s.Resource]) > 1 && count[s.Resource][s.Hash] == most && most*2 <= answering:
				note = "DIFFERS from other nodes"
			case answering > 1:
				note = "same as other nodes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", s.Resource, s.Node, s.Code, s.Hash, note)
		}
	}
	tw.Flush()
}

// LastJob is the newest of the project's recent jobs for the repository
// labelled label (config, code, website x, library x), as tau status names it.
func LastJob(p Patrick, project string, names map[int]string, label string) (*patrick.Job, error) {
	jobs, err := fetch(p, project, recentJobs, map[string]*patrick.Job{})
	if err != nil {
		return nil, err
	}
	var last *patrick.Job
	for _, j := range jobs {
		name := names[j.Meta.Repository.ID]
		if name == "" {
			name = repoLabel(j.Meta.Repository)
		}
		if name == label && (last == nil || j.Timestamp > last.Timestamp) {
			last = j
		}
	}
	if last == nil {
		return nil, fmt.Errorf("no build of %s in the project's last %d jobs; see `tau status`", label, recentJobs)
	}
	return last, nil
}
