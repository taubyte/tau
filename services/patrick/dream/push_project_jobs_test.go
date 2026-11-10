package dream

import (
	"context"
	"testing"

	"github.com/fxamacker/cbor/v2"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	"github.com/taubyte/tau/pkg/kvdb/mock"
	"gotest.tools/v3/assert"
)

// Push-all reads the jobs of its own project, not of the whole universe: a
// universe's thousands of jobs read one by one are what stalled it.
func TestProjectJobsReadsOnlyThatProject(t *testing.T) {
	ctx := context.Background()
	kv, err := mock.New().New(nil, "test", 0)
	assert.NilError(t, err)
	put := func(key string, j *patrickIface.Job) {
		data, err := cbor.Marshal(j)
		assert.NilError(t, err)
		assert.NilError(t, kv.Put(ctx, key, data))
	}
	put("/archive/jobs/done", job(1, 5, "a", patrickIface.JobStatusSuccess))
	put("/jobs/open", job(1, 9, "b", patrickIface.JobStatusOpen))
	put("/archive/jobs/other", job(2, 7, "c", patrickIface.JobStatusSuccess))
	for _, key := range []string{"/by/project/P/done", "/by/project/P/open", "/by/project/Q/other"} {
		assert.NilError(t, kv.Put(ctx, key, []byte{}))
	}

	jobs := newestJobs(projectJobs{ctx: ctx, kv: kv, prefix: "/by/project/P/"})
	assert.Equal(t, len(jobs), 1)
	assert.Equal(t, jobs[1].Meta.HeadCommit.ID, "b", "the newest of the two")
}

// With a project id, push-all takes what that project owns, without asking the
// network about every repository; a repository no project claims is not its.
func TestPushAllTakesOnlyItsProjectsRepositories(t *testing.T) {
	asked := 0
	owner := func(o string) func() string { return func() string { asked++; return o } }
	own := map[string]bool{"1": true}

	assert.Assert(t, inProject(own, "P", "1", owner("P")))
	assert.Assert(t, !inProject(own, "P", "2", owner("")), "an unclaimed repository")
	assert.Assert(t, !inProject(own, "P", "3", owner("Q")), "another project's")
	assert.Equal(t, asked, 0)

	assert.Assert(t, inProject(nil, "", "9", owner("Q")), "no project id: every repository")
	assert.Assert(t, inProject(nil, "P", "2", owner("")), "project unknown to auth: as before")
	assert.Assert(t, !inProject(nil, "P", "3", owner("Q")))
}

// A repository no project claims gets one job, also from a direct push-specific
// and across push-alls whose job list does not hold it.
func TestUnclaimedRepositoryGetsOneJob(t *testing.T) {
	ctx := context.Background()
	kv, err := mock.New().New(nil, "test", 0)
	assert.NilError(t, err)
	unclaimed := func() string { return "" }

	assert.Assert(t, !alreadyPushedUnclaimed(ctx, kv, "7", unclaimed), "its first job")
	noteUnclaimed(ctx, kv, "7", unclaimed)
	assert.Assert(t, alreadyPushedUnclaimed(ctx, kv, "7", unclaimed), "no second job")
	assert.Assert(t, !alreadyPushedUnclaimed(ctx, kv, "7", func() string { return "P" }), "pushed once a project claims it")

	noteUnclaimed(ctx, kv, "8", func() string { return "P" })
	assert.Equal(t, len(unclaimedMarks(ctx, kv)), 1, "a claimed repository is not marked")

	jobs := withUnclaimed(nil, unclaimedMarks(ctx, kv))
	assert.Assert(t, hasJobButNoProject(jobs, "7", unclaimed), "push-all skips the marked repository")
}
