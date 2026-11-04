package cli

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

func TestCIRunQueuesThisCheckoutsHead(t *testing.T) {
	x := newTester(t)
	buildable(t, x)
	x.login()
	x.link("proj-1")
	gitAt(t, x.dir, "init", "-q", "-b", "main")
	gitAt(t, x.dir, "add", "-A")
	gitAt(t, x.dir, "commit", "-q", "-m", "one")
	head := gitAt(t, x.dir, "rev-parse", "HEAD")

	var out ciOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("ci", "run", "--dir", x.dir, "-o", "json")), &out))
	require.Equal(t, "success", out.Status)
	require.Equal(t, []string{"proj-1 github.com/acme/cfg refs/heads/main " + head}, x.f.queued)

	r := x.run("ci", "run", "--dir", x.dir)
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stderr, "queued job")
	require.Contains(t, r.stderr, "stored: built proj-1", "a job that ended before it was followed shows the log it stored")
	require.Contains(t, r.stdout, "success")

	// Named outright, and not followed.
	x.ok("ci", "run", "--dir", x.dir, "--repo", "github.com/acme/pay", "--branch", "main", "--commit", strings.Repeat("a", 40), "--detach")
	require.Equal(t, "proj-1 github.com/acme/pay refs/heads/main "+strings.Repeat("a", 40), x.f.queued[2])
}

func TestCIRunThatFailsOrIsRefused(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")

	x.f.finish = buildv2.JobStatus_JobStatusFailed
	r := x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("b", 40), "--branch", "main")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "failed")

	r = x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("b", 40), "--branch", "feature")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "not a ref this cloud builds")

	r = x.run("ci", "run", "--dir", x.dir, "--branch", "main")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, `no branch "main"; pass --commit`)

	unlinked := newTester(t)
	unlinked.login()
	r = unlinked.run("ci", "run", "--dir", unlinked.dir, "--project", "proj-1")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "--repo is required")

	r = unlinked.run("ci", "run", "--dir", unlinked.dir)
	require.NotZero(t, r.code, "no project to build")

	loggedOut := newTester(t)
	loggedOut.link("proj-1")
	r = loggedOut.run("ci", "run", "--dir", loggedOut.dir, "--commit", strings.Repeat("b", 40), "--branch", "main")
	require.NotZero(t, r.code)
}

func TestCIRunFollowsARunningJob(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")
	x.f.finish = buildv2.JobStatus_JobStatusClaimed
	x.f.endOnFollow = true
	r := x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("c", 40), "--branch", "main", "--interval", "10ms")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stderr, "built proj-1")
	require.NotContains(t, r.stderr, "stored:", "it was followed live, so the stored log is not printed again")
}

func TestCIRunFollowsARerunFromItsStart(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")
	x.f.finish = buildv2.JobStatus_JobStatusClaimed
	x.f.endOnFollow = true
	x.f.mu.Lock()
	x.f.dropOnce, x.f.rerunOnDrop = true, true
	x.f.runLogs["job-3-prod/2"] = "rebuilt from its start\n"
	x.f.mu.Unlock()

	r := x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("c", 40), "--branch", "main", "--interval", "10ms")
	require.Zero(t, r.code, r.stderr)
	built := "built proj-1 github.com/acme/cfg refs/heads/main " + strings.Repeat("c", 40) + "\n"
	require.Contains(t, r.stderr, built+"job job-3-prod: the log of attempt 1 was not kept past byte "+strconv.Itoa(len(built))+"\nrebuilt from its start\n")
	require.Contains(t, r.stdout, "success")
}

func TestCIRunMovesOnFromARunItCouldNotRead(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")
	x.f.finish = buildv2.JobStatus_JobStatusClaimed
	x.f.endOnFollow = true
	x.f.mu.Lock()
	x.f.refused["job-3-prod/1"], x.f.rerunOnDrop = true, true
	x.f.runLogs["job-3-prod/2"] = "rebuilt\n"
	x.f.mu.Unlock()
	r := x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("c", 40), "--branch", "main", "--interval", "10ms")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stderr, "job job-3-prod: the log of attempt 1 was read to byte 0; attempt 2 follows\nrebuilt\n")

	x.f.mu.Lock()
	x.f.finish, x.f.rerunOnDrop = buildv2.JobStatus_JobStatusSuccess, false
	x.f.refused["job-4-prod/1"] = true
	x.f.mu.Unlock()
	r = x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("d", 40), "--branch", "main", "--interval", "10ms")
	require.Zero(t, r.code, r.stderr)
	require.Contains(t, r.stderr, "the job's log cannot be read: the cloud answered 503: no build node available")

	// A log that does not say which run it is cannot be followed at all.
	x.f.mu.Lock()
	x.f.finish, x.f.unnamed = buildv2.JobStatus_JobStatusClaimed, true
	x.f.mu.Unlock()
	r = x.run("ci", "run", "--dir", x.dir, "--commit", strings.Repeat("e", 40), "--branch", "main", "--interval", "10ms")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "did not say which run")
}
