package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
)

// A BUILD BY HAND names the linked configuration repository, the checked-out
// branch and its head — all resolved here — and follows the job to its end.
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

// A JOB THAT FAILS fails the command; what the cloud refuses is its reason.
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

// A RUNNING JOB IS FOLLOWED LIVE, and its end read from its record.
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
