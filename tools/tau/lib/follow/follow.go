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
	"path"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
)

// Patrick is the part of the Patrick client this package reads.
type Patrick interface {
	JobsLast(projectId string, last int) ([]string, error)
	Job(jid string) (*patrick.Job, error)
	LogFile(jobId, resourceId string) (io.ReadCloser, error)
}

// Repo is a pushed repository and the commit its build must be for.
type Repo struct {
	Name   string
	Commit string
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

// Wait follows the newest job of each repo's pushed commit until all end.
// Matching on the commit, not the time, keeps it right under clock skew.
// Exit code: 0 all succeeded, 1 one failed or was canceled, 2 ctx ended first.
func Wait(ctx context.Context, p Patrick, project string, repos []Repo, poll time.Duration) ([]Job, int) {
	cache := map[string]*patrick.Job{}
	out := make([]Job, len(repos))
	for i, r := range repos {
		out[i] = Job{Repo: r.Name, Status: "no job"}
	}
	for {
		jobs, _ := fetch(p, project, recentJobs, cache) // a missed poll is retried
		done := 0
		for i, r := range repos {
			var newest *patrick.Job
			for _, j := range jobs {
				if j.Meta.HeadCommit.ID == r.Commit && (newest == nil || j.Timestamp > newest.Timestamp) {
					newest = j
				}
			}
			if newest == nil {
				continue
			}
			prev := out[i]
			out[i] = Job{Repo: r.Name, Id: newest.Id, Status: status(newest.Status), Attempt: newest.Attempt,
				Seconds: time.Since(time.Unix(newest.Timestamp, 0)).Round(time.Second).Seconds()}
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
		fmt.Fprintf(w, "%-8s %-8s attempt %d  %4.0fs  %s\n", j.Repo, j.Status, j.Attempt, j.Seconds, j.Id)
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
}

// Probe GETs the target; code 0 means it did not answer.
func Probe(t Target) (int, []byte) {
	req, err := http.NewRequest(http.MethodGet, t.URL, nil)
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
}

// Serve polls every target until what it serves differs from before(target),
// i.e. the node moved to the new build. Exit code 0, or 2 if ctx ended first.
func Serve(ctx context.Context, targets []Target, before func(Target) string, poll time.Duration) ([]Served, int) {
	start := time.Now()
	out := make([]Served, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			old := before(t)
			out[i] = Served{Resource: t.Resource, Node: t.Node}
			for {
				code, body := Probe(t)
				out[i].Code, out[i].Hash = code, Hash(code, body)
				if code != 0 && out[i].Hash != old {
					out[i].Changed = true
					out[i].Seconds = time.Since(start).Round(100 * time.Millisecond).Seconds()
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(poll):
				}
			}
		}()
	}
	wg.Wait()
	for _, s := range out {
		if !s.Changed {
			return out, 2
		}
	}
	return out, 0
}

// WriteServed prints per node how long it took to serve the new build.
func WriteServed(w io.Writer, served []Served) {
	for _, s := range served {
		if s.Changed {
			fmt.Fprintf(w, "%-20s %-24s serves new build after %.1fs (HTTP %d)\n", s.Resource, s.Node, s.Seconds, s.Code)
		} else {
			fmt.Fprintf(w, "%-20s %-24s unchanged (HTTP %d)\n", s.Resource, s.Node, s.Code)
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
			name = repoName(j.Meta.Repository)
		}
		r := RepoStatus{Repo: name, Job: j.Id, Status: status(j.Status), Attempt: j.Attempt,
			AgeSeconds: time.Since(time.Unix(j.Timestamp, 0)).Round(time.Second).Seconds()}
		if j.Status == patrick.JobStatusFailed {
			r.Error, _ = logTail(p, j)
		}
		rep.Repos = append(rep.Repos, r)
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
	u = u[strings.LastIndexAny(u, ":/")+1:]
	return strings.TrimSuffix(path.Base(u), ".git")
}

// WriteStatus prints the report as two tables. A node whose answer differs
// from the other nodes of the same resource is marked: it is behind or ahead.
func WriteStatus(w io.Writer, rep Report) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REPO\tSTATUS\tATTEMPT\tAGE\tJOB")
	for _, r := range rep.Repos {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", r.Repo, r.Status, r.Attempt, time.Duration(r.AgeSeconds)*time.Second, r.Job)
		if r.Error != "" {
			fmt.Fprintf(tw, "  error: %s\t\t\t\t\n", r.Error)
		}
	}
	if len(rep.Serving) > 0 {
		count := map[string]map[string]int{}
		for _, s := range rep.Serving {
			if count[s.Resource] == nil {
				count[s.Resource] = map[string]int{}
			}
			count[s.Resource][s.Hash]++
		}
		fmt.Fprintln(tw, "\t\t\t\t")
		fmt.Fprintln(tw, "RESOURCE\tNODE\tHTTP\tSERVES\t")
		for _, s := range rep.Serving {
			note := "same as other nodes"
			switch {
			case s.Code == 0:
				note = "no answer"
			case len(count[s.Resource]) > 1:
				note = "DIFFERS from other nodes"
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", s.Resource, s.Node, s.Code, s.Hash, note)
		}
	}
	tw.Flush()
}
