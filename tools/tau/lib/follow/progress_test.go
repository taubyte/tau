package follow

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// A line as the job appears, as it starts, on a new attempt, and as it ends.
func TestWaitPrintsProgress(t *testing.T) {
	f, p := newPatrick(t)
	step := func(status patrick.JobStatus, attempt int) {
		time.Sleep(40 * time.Millisecond)
		j := job("j1", "c1", 1, status)
		j.Attempt = attempt
		f.set(j)
	}
	go func() {
		step(patrick.JobStatusOpen, 0)
		step(patrick.JobStatusLocked, 1)
		step(patrick.JobStatusLocked, 2)
		step(patrick.JobStatusSuccess, 2)
	}()

	var out syncBuf
	_, code := Wait(context.Background(), p, "p", []Repo{{Name: "code", Commit: "c1"}}, poll, &out)
	assert.Equal(t, code, 0)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	assert.Equal(t, len(lines), 4, out.String())
	for i, want := range []string{"queued", "building attempt 1", "building attempt 2", "success"} {
		assert.Assert(t, strings.Contains(lines[i], want), "line %d %q", i, lines[i])
		assert.Assert(t, strings.Contains(lines[i], "j1"), lines[i])
	}
}

// Dream's injected job carries a placeholder commit: it is matched by repo
// name and time; an exact commit match still wins.
func TestWaitMatchesDreamJobWithoutCommit(t *testing.T) {
	since := time.Now().Unix()
	old := job("old", "0000", 1, patrick.JobStatusSuccess)
	old.Timestamp = since - 100
	_, p := newPatrick(t, old, job("dream", "0000", 1, patrick.JobStatusSuccess))

	repo := Repo{Name: "code", Commit: "c1", Remote: "tb_code_1", Since: since}
	jobs, code := Wait(context.Background(), p, "p", []Repo{repo}, poll, nil)
	assert.Equal(t, code, 0)
	assert.Equal(t, jobs[0].Id, "dream")

	exact := job("exact", "c1", 1, patrick.JobStatusSuccess)
	exact.Timestamp = since - 1
	_, p = newPatrick(t, job("dream", "0000", 1, patrick.JobStatusSuccess), exact)
	jobs, _ = Wait(context.Background(), p, "p", []Repo{repo}, poll, nil)
	assert.Equal(t, jobs[0].Id, "exact")
}

// A finished job is timed from the push, not by its age.
func TestWaitSecondsFromPush(t *testing.T) {
	j := job("j1", "c1", 1, patrick.JobStatusSuccess)
	j.Timestamp = time.Now().Unix() - 500
	_, p := newPatrick(t, j)
	jobs, _ := Wait(context.Background(), p, "p", []Repo{{Name: "code", Commit: "c1", Since: time.Now().Unix() - 10}}, poll, nil)
	assert.Assert(t, jobs[0].Seconds >= 9 && jobs[0].Seconds <= 12, "%v", jobs[0].Seconds)
}

// C1: a job Patrick keeps "open" until it ends gets a heartbeat line.
func TestWaitHeartbeat(t *testing.T) {
	old := heartbeat
	heartbeat = 30 * time.Millisecond
	t.Cleanup(func() { heartbeat = old })
	f, p := newPatrick(t, job("j1", "c1", 1, patrick.JobStatusOpen))
	var out syncBuf
	go func() { // end the job once a heartbeat was printed (or after 5s)
		for i := 0; i < 500 && !strings.Contains(out.String(), "still building"); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		f.set(job("j1", "c1", 1, patrick.JobStatusSuccess))
	}()
	_, code := Wait(context.Background(), p, "p", []Repo{{Name: "code", Commit: "c1"}}, poll, &out)
	assert.Equal(t, code, 0)
	assert.Assert(t, strings.Contains(out.String(), "code     still building, "), out.String())
	assert.Assert(t, strings.Contains(out.String(), "after push  job j1"), out.String())
}

// C2: the job's one final line is Wait's, with attempt and error; push
// printed a second one from WriteJobs.
func TestWaitFinalLineHasError(t *testing.T) {
	f, p := newPatrick(t)
	j := job("j1", "c1", 1, patrick.JobStatusFailed)
	j.Logs = map[string]string{"r": "l"}
	f.logs["l"] = "error: boom"
	f.set(j)
	var out syncBuf
	Wait(context.Background(), p, "p", []Repo{{Name: "code", Commit: "c1"}}, poll, &out)
	s := out.String()
	assert.Equal(t, strings.Count(s, "j1"), 1, s)
	assert.Assert(t, strings.Contains(s, "failed   attempt 1"), s)
	assert.Assert(t, strings.Contains(s, "error: error: boom"), s)
}
