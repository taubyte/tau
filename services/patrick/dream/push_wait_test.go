package dream

import (
	"context"
	"sync"
	"testing"
	"time"

	patrickIface "github.com/taubyte/tau/core/services/patrick"
	"gotest.tools/v3/assert"
)

// lockedPatrick lets a test add jobs while waitForJob polls.
type lockedPatrick struct {
	lock sync.Mutex
	fakePatrick
}

func (p *lockedPatrick) List() ([]string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.fakePatrick.List()
}

func (p *lockedPatrick) Get(id string) (*patrickIface.Job, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.fakePatrick.Get(id)
}

func (p *lockedPatrick) add(id string, j *patrickIface.Job) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.jobs[id] = j
}

// A website added to an imported project's config is only listed once the
// config job has published: push-all must wait for that job to finish.
func TestPushAllWaitsForConfigJob(t *testing.T) {
	ctx := context.Background()
	p := &lockedPatrick{fakePatrick: fakePatrick{jobs: map[string]*patrickIface.Job{
		"old": job(1, 5, "a", patrickIface.JobStatusSuccess), // an earlier build
		"new": job(1, 10, "b", patrickIface.JobStatusLocked), // this push's job, building
	}}}
	assert.Assert(t, !waitForJob(ctx, p, 1, 10, 0), "config job still building")
	p.add("new", job(1, 10, "b", patrickIface.JobStatusSuccess))
	assert.Assert(t, waitForJob(ctx, p, 1, 10, 0), "config job done")
	assert.Assert(t, !waitForJob(ctx, p, 1, 11, 0), "a job older than the push does not count")

	go func() {
		time.Sleep(1500 * time.Millisecond)
		p.add("late", job(2, 20, "c", patrickIface.JobStatusFailed))
	}()
	assert.Assert(t, waitForJob(ctx, p, 2, 20, 5*time.Second), "waits until the job finishes")
}
