package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

// A new website or library repository belongs to a project only once the
// config job has published it. Its first push must not fail the webhook or
// run before then: the job waits, and the leader queues it, linked to its
// project, as soon as the project is known.
func TestFirstPushOfANewRepositoryWaitsForItsProject(t *testing.T) {
	ctx := context.Background()
	db, _ := mock.New().New(nil, "test", 0)
	auth := &mockAuthClient{repos: make(map[int]mockRepo)}
	tns := &mockTNSClient{lookupError: errors.New("response from lookup not an array or is empty: `[]`")}
	queue := &mockJobQueue{}
	srv := &PatrickService{db: db, authClient: auth, tnsClient: tns, jobQueue: queue}

	job := createTestJob("website-job")
	assert.NilError(t, srv.RegisterJob(ctx, job), "the webhook must not fail while the project is unknown")
	assert.Equal(t, len(queue.pushCalls), 0, "queued before its project is known")

	assert.NilError(t, srv.ReannounceJobs(ctx))
	assert.Equal(t, len(queue.pushCalls), 0, "the sweep queued it before its project is known")

	// the config job publishes the repository
	tns.lookupError = nil
	tns.lookupResponse = []string{"repositories/github/12345/extra/project-456"}

	assert.NilError(t, srv.ReannounceJobs(ctx))
	assert.Equal(t, len(queue.pushCalls), 1)
	assert.Equal(t, queue.pushCalls[0].id, job.Id)
	_, err := db.Get(ctx, "/by/project/project-456/"+job.Id)
	assert.NilError(t, err, "not listed under its project")

	assert.NilError(t, srv.ReannounceJobs(ctx))
	assert.Equal(t, len(queue.pushCalls), 2, "a linked job is re-announced as before")
}

// A repository no project ever claims is not held forever: after
// projectWaitMax it is queued as before and fails with the lookup error.
func TestWaitingForAProjectEnds(t *testing.T) {
	ctx := context.Background()
	db, _ := mock.New().New(nil, "test", 0)
	tns := &mockTNSClient{lookupError: errors.New("empty")}
	queue := &mockJobQueue{}
	srv := &PatrickService{db: db, authClient: &mockAuthClient{repos: make(map[int]mockRepo)}, tnsClient: tns, jobQueue: queue}

	defer func(d time.Duration) { projectWaitMax = d }(projectWaitMax)
	projectWaitMax = 0

	assert.NilError(t, srv.RegisterJob(ctx, createTestJob("orphan")))
	assert.NilError(t, srv.ReannounceJobs(ctx))
	assert.Equal(t, len(queue.pushCalls), 1)
}
