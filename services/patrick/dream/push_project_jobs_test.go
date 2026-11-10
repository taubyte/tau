package dream

import (
	"context"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	authIface "github.com/taubyte/tau/core/services/auth"
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

// A project's push-all takes only what that project owns: never a repository
// merely because nobody has claimed it.
func TestPushAllTakesOnlyItsProjectsRepositories(t *testing.T) {
	own := map[string]bool{"1": true}

	assert.Assert(t, inProject(own, "P", "1"))
	assert.Assert(t, !inProject(own, "P", "2"), "an unclaimed repository")
	assert.Assert(t, !inProject(nil, "P", "2"), "ownership unknown: nothing is taken")
	assert.Assert(t, inProject(nil, "", "9"), "no project id: every repository")
}

// A repository its project owns is claimed, whatever a lookup says: a new
// project's repositories were skipped as unclaimed, so they never got a job.
func TestAProjectsOwnRepositoryIsClaimed(t *testing.T) {
	asked := 0
	lookup := func() string { asked++; return "" }
	own := map[string]bool{"1": true}

	assert.Equal(t, claimedBy(own, "P", "1", lookup)(), "P")
	assert.Equal(t, asked, 0)
	assert.Equal(t, claimedBy(own, "P", "2", lookup)(), "", "not its own: the lookup decides")
	assert.Equal(t, asked, 1)

	jobs := map[int]*patrickIface.Job{1: {}}
	pushed := map[string]bool{}
	assert.Assert(t, markPushed(pushed, jobs, "1", claimedBy(own, "P", "1", lookup)), "pushed despite an earlier job")
}

// Just after Dream starts, Auth does not answer yet: the project is waited for.
func TestProjectIsWaitedFor(t *testing.T) {
	ctx := context.Background()
	calls := 0
	got := waitForAuthProject(ctx, func() *authIface.Project {
		if calls++; calls < 3 {
			return nil
		}
		return &authIface.Project{Id: "P"}
	}, 10*time.Second)
	assert.Assert(t, got != nil && got.Id == "P")
	assert.Equal(t, calls, 3)

	assert.Assert(t, waitForAuthProject(ctx, func() *authIface.Project { return nil }, 0) == nil, "unknown after the wait")
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
