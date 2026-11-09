package service

import (
	"context"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

func storedJob(t *testing.T, id string) (*PatrickService, context.Context) {
	ctx := context.Background()
	db, err := mock.New().New(nil, "test", 0)
	assert.NilError(t, err)
	data, err := cbor.Marshal(createTestJob(id))
	assert.NilError(t, err)
	assert.NilError(t, db.Put(ctx, "/jobs/"+id, data))
	return &PatrickService{db: db}, ctx
}

// A failure with a try left is not the job's result: it reads as open, and
// only the last failure reads as failed.
func TestFailedJobWithATryLeftIsOpen(t *testing.T) {
	srv, ctx := storedJob(t, "j1")

	assert.NilError(t, srv.updateStatus(ctx, "", "j1", nil, patrick.JobStatusFailed, nil, ""))
	job, err := srv.getJob(ctx, "/jobs/", "j1")
	assert.NilError(t, err)
	assert.Equal(t, job.Status, patrick.JobStatusOpen)
	assert.Equal(t, job.Attempt, 1)

	assert.NilError(t, srv.updateStatus(ctx, "", "j1", nil, patrick.JobStatusFailed, nil, ""))
	job, err = srv.getJob(ctx, "/archive/jobs/", "j1")
	assert.NilError(t, err)
	assert.Equal(t, job.Status, patrick.JobStatusFailed)
}

// A build that cannot succeed on another try is failed on its first.
func TestFailedJobWithAReasonIsNotRetried(t *testing.T) {
	srv, ctx := storedJob(t, "oom")

	assert.NilError(t, srv.updateStatus(ctx, "", "oom", nil, patrick.JobStatusFailed, nil, "out of memory"))
	job, err := srv.getJob(ctx, "/archive/jobs/", "oom")
	assert.NilError(t, err)
	assert.Equal(t, job.Status, patrick.JobStatusFailed)
	_, err = srv.getJob(ctx, "/jobs/", "oom")
	assert.Assert(t, err != nil, "still queued for another run")
}
