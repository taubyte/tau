package dream

import (
	"errors"
	"testing"

	patrickIface "github.com/taubyte/tau/core/services/patrick"
	commonTest "github.com/taubyte/tau/dream/helpers"
	"gotest.tools/v3/assert"
)

type fakePatrick struct {
	patrickIface.Client
	jobs map[string]*patrickIface.Job
}

func (p fakePatrick) List() (ids []string, _ error) {
	for id := range p.jobs {
		ids = append(ids, id)
	}
	return ids, nil
}

func (p fakePatrick) Get(id string) (*patrickIface.Job, error) {
	if j, ok := p.jobs[id]; ok {
		return j, nil
	}
	return nil, errors.New("not found")
}

func job(repo int, ts int64, commit string, status patrickIface.JobStatus) *patrickIface.Job {
	j := &patrickIface.Job{Timestamp: ts, Status: status}
	j.Meta.Repository.ID = repo
	j.Meta.HeadCommit.ID = commit
	return j
}

func TestPushAllSkipsBuiltCommits(t *testing.T) {
	jobs := newestJobs(fakePatrick{jobs: map[string]*patrickIface.Job{
		"a1": job(1, 1, "old", patrickIface.JobStatusFailed),
		"a2": job(1, 2, "abc", patrickIface.JobStatusSuccess), // newest of repo 1
		"b1": job(2, 2, "abc", patrickIface.JobStatusSuccess),
		"b2": job(2, 3, "abc", patrickIface.JobStatusFailed), // newest of repo 2 failed
		"c1": job(3, 1, commonTest.DefaultCommitID, patrickIface.JobStatusSuccess),
	}})
	assert.Assert(t, built(jobs, "1", "abc"))
	assert.Assert(t, !built(jobs, "1", "new"), "a new commit rebuilds")
	assert.Assert(t, !built(jobs, "2", "abc"), "newest job failed")
	assert.Assert(t, !built(jobs, "3", commonTest.DefaultCommitID), "default commit always rebuilds")
	assert.Assert(t, !built(jobs, "4", "abc"), "never built")
	assert.Assert(t, !built(nil, "1", "abc"), "no patrick: push")
}

func TestPushAllGivesAnUnclaimedRepositoryOneJob(t *testing.T) {
	jobs := map[int]*patrickIface.Job{7: job(7, 1, "c", patrickIface.JobStatusFailed)}
	unclaimed := func() string { return "" }

	assert.Assert(t, hasJobButNoProject(jobs, "7", unclaimed), "an orphan with a job got another")
	assert.Assert(t, !hasJobButNoProject(jobs, "8", unclaimed), "a new repository gets its first job")
	assert.Assert(t, !hasJobButNoProject(jobs, "7", func() string { return "project" }), "a repository that reached TNS is pushed")
	assert.Assert(t, !hasJobButNoProject(nil, "7", unclaimed), "force pushes everything")
}

// A repository skipped in the first pass of a push-all stays eligible for the
// second: after the config job, a project may claim it.
func TestPushAllSecondPassPushesARepositorySkippedInTheFirst(t *testing.T) {
	jobs := map[int]*patrickIface.Job{7: job(7, 1, "c", patrickIface.JobStatusFailed)}
	pushed := map[string]bool{}

	assert.Assert(t, !markPushed(pushed, jobs, "7", func() string { return "" }), "skipped while unclaimed")
	assert.Assert(t, !pushed["7"], "a skipped repository is not pushed yet")
	assert.Assert(t, markPushed(pushed, jobs, "7", func() string { return "project" }), "pushed once claimed")
	assert.Assert(t, pushed["7"])
}
