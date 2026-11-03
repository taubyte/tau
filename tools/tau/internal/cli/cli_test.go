package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	buildv2 "github.com/taubyte/tau/pkg/proto/gen/tau/build/v2"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

func TestLoginStoresTheTokenPrivately(t *testing.T) {
	x := newTester(t)
	x.login()

	path, err := config.Path()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dir, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dir.Mode().Perm())

	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, goodToken, cfg.Profiles["default"].Token)
	require.Equal(t, "default", cfg.Current)

	golden(t, "profiles", strings.ReplaceAll(x.ok("profiles"), x.f.srv.URL, "URL"))
	var rows []profileRow
	require.NoError(t, json.Unmarshal([]byte(x.ok("profiles", "-o", "json")), &rows))
	require.True(t, rows[0].Current)
	require.True(t, rows[0].LoggedIn)
}

func TestLoginFromStdinAndASecondProfile(t *testing.T) {
	x := newTester(t)
	x.login()
	x.stdin = goodToken + "\n"
	x.ok("login", "--profile", "staging", "--cloud", x.f.srv.URL, "--token-stdin", "--use=false")

	cfg, err := loadConfig()
	require.NoError(t, err)
	require.Equal(t, "default", cfg.Current)
	require.Equal(t, goodToken, cfg.Profiles["staging"].Token)

	x.ok("profiles", "staging")
	cfg, _ = loadConfig()
	require.Equal(t, "staging", cfg.Current)
	require.NotZero(t, x.run("profiles", "nope").code)

	x.ok("logout")
	cfg, _ = loadConfig()
	require.Empty(t, cfg.Profiles["staging"].Token)
	require.Contains(t, x.ok("profiles"), "(logged out)")
	require.NotZero(t, x.run("logout", "--profile", "nope").code)
}

func TestLoginRefusals(t *testing.T) {
	x := newTester(t)
	r := x.run("login", "--token", goodToken)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "--cloud is required")

	r = x.run("login", "--cloud", "ftp://nope", "--token", goodToken)
	require.Contains(t, r.stderr, "not a cloud URL")

	r = x.run("login", "--cloud", x.f.srv.URL)
	require.Contains(t, r.stderr, "say how to sign in")

	r = x.run("login", "--cloud", x.f.srv.URL, "--token", "tok-bad")
	require.Contains(t, r.stderr, "unauthenticated")

	x.stdin = ""
	r = x.run("login", "--cloud", x.f.srv.URL, "--token-stdin")
	require.Contains(t, r.stderr, "nothing there")

	x.stdin = "  \n"
	r = x.run("login", "--cloud", x.f.srv.URL, "--token-stdin")
	require.Contains(t, r.stderr, "empty")

	r = x.run("profiles", "-o", "yaml")
	require.Contains(t, r.stderr, "--output is text or json")

	require.Contains(t, x.ok("profiles"), "no profiles")
}

func TestLoginPromptsAtATerminal(t *testing.T) {
	x := newTester(t)
	x.interactive = true
	x.prompter.answers = []string{x.f.srv.URL, "password", "root", "hunter2"}
	x.ok("login")
	require.Equal(t, []string{"Cloud URL (the build frontend)", "Sign in with", "User", "Password"}, x.prompter.asked)
}

func TestInitValidateViews(t *testing.T) {
	x := newTester(t)
	out := x.ok("init", "shop", "--dir", x.dir)
	require.Contains(t, out, `created project "shop"`)
	require.FileExists(t, filepath.Join(x.dir, "project.yaml"))

	golden(t, "validate-starter", x.ok("validate", "--dir", x.dir))
	golden(t, "views-starter", x.ok("views", "--dir", x.dir))

	type lintReport struct {
		Error    string `json:"error"`
		Findings []any  `json:"findings"`
	}
	var report lintReport
	require.NoError(t, json.Unmarshal([]byte(x.ok("validate", "--dir", x.dir, "-o", "json")), &report))
	require.Empty(t, report.Error)

	r := x.run("init", "shop", "--dir", x.dir)
	require.Contains(t, r.stderr, "already holds a project")
	x.ok("init", "shop", "--dir", x.dir, "--force")

	require.Contains(t, x.run("init", "--dir", t.TempDir()).stderr, "project name is required")
	x.interactive = true
	x.prompter.answers = []string{"other"}
	x.ok("init", "--dir", t.TempDir())
}

func TestValidateFailsABrokenProject(t *testing.T) {
	x := newTester(t)
	x.ok("init", "shop", "--dir", x.dir)
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "function", "hello.yaml"), []byte("description: no id\n"), 0o644))
	r := x.run("validate", "--dir", x.dir)
	require.Equal(t, ExitBroken, r.code)
	require.Contains(t, r.stdout, "does not compile")

	require.Contains(t, x.run("validate", "--dir", t.TempDir()).stderr, "no project here")
	require.NotZero(t, x.run("views", "--dir", x.dir).code)
	require.NotZero(t, x.run("views", "--dir", filepath.Join(x.dir, "missing")).code)
	require.NotZero(t, x.run("validate", "--dir", filepath.Join(x.dir, "missing")).code)
}

func TestNewListsKindsFromTheSchema(t *testing.T) {
	x := newTester(t)
	golden(t, "kinds", x.ok("new"))
	var kinds []map[string]any
	require.NoError(t, json.Unmarshal([]byte(x.ok("new", "-o", "json")), &kinds))
	require.NotEmpty(t, kinds)
}

func TestNewWritesAResourceAndRefusesAnIncompleteOne(t *testing.T) {
	x := newTester(t)
	x.ok("init", "shop", "--dir", x.dir)

	r := x.run("new", "function", "bye", "--dir", x.dir)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "--set package.name=", "what it runs is the author's to say")
	require.NoFileExists(t, filepath.Join(x.dir, "function", "bye.yaml"))

	out := x.ok("new", "function", "bye", "--dir", x.dir,
		"--set", "package.name=hello_code", "--set", "execution.call=handle",
		"--set", "execution.timeout=10s", "--set", "execution.memory=16MB")
	require.Contains(t, out, "created function/bye")
	require.FileExists(t, filepath.Join(x.dir, "function", "bye.yaml"))
	require.Zero(t, x.run("validate", "--dir", x.dir).code)

	out = x.ok("new", "function", "stub", "--dir", x.dir, "--incomplete", "-o", "json")
	require.Contains(t, out, `"issues"`)

	require.Contains(t, x.run("new", "function", "--dir", x.dir).stderr, "a name is required")
	require.Contains(t, x.run("new", "function", "x", "--dir", x.dir, "--set", "nonsense").stderr, "not path=value")
	require.Contains(t, x.run("new", "function", "x", "--dir", t.TempDir()).stderr, "no project here")
	require.NotZero(t, x.run("new", "function", "x", "--dir", filepath.Join(x.dir, "missing")).code)
	require.NotZero(t, x.run("new", "nokind", "x", "--dir", x.dir).code)
	require.NotZero(t, x.run("new", "function", "x", "--dir", x.dir, "--set", "execution.memory=lots").code)
}

func TestNewPromptsForWhatTheResourceOwes(t *testing.T) {
	x := newTester(t)
	x.ok("init", "shop", "--dir", x.dir)
	x.interactive = true
	x.prompter.answers = []string{"function", "bye", "hello_code", "10s", "16MB"}
	out := x.ok("new", "--dir", x.dir, "--set", "execution.call=handle")
	require.Contains(t, out, "created function/bye")
	require.Equal(t, []string{"Kind", "Name", "package.name", "execution.timeout", "execution.memory"}, x.prompter.asked)

	x.prompter.answers = []string{"function", "bad", "hello_code", "10s", "lots"}
	require.NotZero(t, x.run("new", "--dir", x.dir).code)

	x.prompter.err = errors.New("interrupted")
	require.NotZero(t, x.run("new", "--dir", x.dir).code)
	require.NotZero(t, x.run("new", "function", "--dir", x.dir).code)
	require.NotZero(t, x.run("new", "function", "y", "--dir", x.dir).code)
	require.NotZero(t, x.run("init", "--dir", t.TempDir()).code)
}

func TestStatus(t *testing.T) {
	x := newTester(t)
	golden(t, "status-nothing", x.ok("status", "--dir", x.dir))

	x.login()
	x.link("proj-1")
	golden(t, "status-jobs", strings.ReplaceAll(x.ok("status", "--dir", x.dir), x.f.srv.URL, "URL"))
	require.Contains(t, x.ok("status", "--dir", x.dir, "-o", "json"), `"project_id": "proj-1"`)

	x.link("proj-unknown")
	require.Contains(t, x.ok("status", "--dir", x.dir), "not found")
}

func TestJobs(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")

	golden(t, "jobs", x.ok("jobs", "--dir", x.dir))
	var jobs []jobView
	require.NoError(t, json.Unmarshal([]byte(x.ok("jobs", "--dir", x.dir, "-o", "json")), &jobs))
	require.Equal(t, "job-2", jobs[0].ID, "newest first")
	require.Equal(t, "failed", jobs[0].Status)

	require.Len(t, mustJobs(t, x.ok("jobs", "--project", "proj-1", "--limit", "1", "-o", "json")), 1)
	require.Contains(t, x.run("jobs", "--dir", t.TempDir()).stderr, "not linked")
	require.Contains(t, x.run("jobs", "--project", "proj-nope").stderr, "not found")

	x.f.byProj["proj-empty"] = nil
	golden(t, "jobs-empty", x.ok("jobs", "--project", "proj-empty"))

	x.f.byProj["proj-ghost"] = []string{"ghost"}
	require.NotZero(t, x.run("jobs", "--project", "proj-ghost").code)
}

func mustJobs(t *testing.T, s string) []jobView {
	var jobs []jobView
	require.NoError(t, json.Unmarshal([]byte(s), &jobs))
	return jobs
}

func TestJob(t *testing.T) {
	x := newTester(t)
	x.login()
	golden(t, "job", x.ok("job", "job-1"))
	require.Contains(t, x.ok("job", "job-1", "-o", "json"), `"status": "success"`)
	require.Contains(t, x.run("job").stderr, "job id is required")
	require.Contains(t, x.run("job", "nope").stderr, "not found")
}

func TestLogs(t *testing.T) {
	x := newTester(t)
	x.login()
	// A FINISHED JOB'S LOG IS THE ONE IT STORED: no node is writing it any
	// more, so following it would read nothing.
	require.Equal(t, "stored: step one\nstep two\n", x.ok("logs", "job-1"))
	require.Equal(t, "step two\n", x.ok("logs", "job-1", "--offset", "17"))
	require.Equal(t, "", x.ok("logs", "job-1", "--offset", "999"))

	require.Equal(t, "step one\nstep two\n", x.ok("logs", "job-live"))
	require.Equal(t, "step two\n", x.ok("logs", "job-live", "--offset", "9"))

	// A dropped connection resumes at the byte offset reached, so nothing is
	// printed twice and nothing is lost.
	x.f.dropOnce = true
	require.Equal(t, "step one\nstep two\n", x.ok("logs", "job-live", "--follow"))

	// At a terminal, --follow draws the live view and reads the same bytes.
	x.interactive = true
	x.ok("logs", "job-live", "-f")

	require.NotZero(t, x.run("logs").code)
	require.Contains(t, x.run("logs", "nope").stderr, "not found")
	require.Contains(t, x.run("logs", "nope", "-f").stderr, "not found")
}

func TestLogsOfAJobStillRunning(t *testing.T) {
	x := newTester(t)
	x.login()
	job := x.f.building(t, "job-run", "step one\n")

	soFar := func(args ...string) string {
		t.Helper()
		var out syncBuffer
		select {
		case code := <-x.start(&out, args...):
			require.Equal(t, 0, code)
		case <-time.After(10 * time.Second):
			t.Fatal("tau logs without --follow waited for the job to end")
		}
		return out.String()
	}
	require.Equal(t, "step one\n", soFar("logs", "job-run"))

	var followed syncBuffer
	exited := x.start(&followed, "logs", "job-run", "--follow")
	require.Eventually(t, func() bool { return followed.String() == "step one\n" }, 10*time.Second, 5*time.Millisecond)

	job.say("step two\n")
	require.Eventually(t, func() bool { return followed.String() == "step one\nstep two\n" }, 10*time.Second, 5*time.Millisecond)
	select {
	case code := <-exited:
		t.Fatalf("tau logs --follow ended (%d) while the job was running", code)
	default:
	}
	require.Equal(t, "step one\nstep two\n", soFar("logs", "job-run"))
	require.Equal(t, "step two\n", soFar("logs", "job-run", "--offset", "9"))

	job.end()
	select {
	case code := <-exited:
		require.Equal(t, 0, code)
	case <-time.After(10 * time.Second):
		t.Fatal("tau logs --follow outlived the job")
	}
	require.Equal(t, "step one\nstep two\n", followed.String())

	require.Equal(t, "stored: step one\nstep two\n", x.ok("logs", "job-run"))
	require.Equal(t, "stored: step one\nstep two\n", x.ok("logs", "job-run", "--follow"))
}

func TestLogsOfAQueuedJob(t *testing.T) {
	x := newTester(t)
	x.login()
	job := x.f.queuedJob(t, "job-queued")

	r := x.run("logs", "job-queued")
	require.Equal(t, 0, r.code, r.stderr)
	require.Empty(t, r.stdout)
	require.Equal(t, "job job-queued is queued: it has logged nothing yet\n", r.stderr)

	quiet := x.run("logs", "job-queued", "-o", "json")
	require.Equal(t, 0, quiet.code, quiet.stderr)
	require.Empty(t, quiet.stderr, "a note is not part of a machine's output")

	var followed, notes syncBuffer
	exited := x.startWith(&followed, &notes, "logs", "job-queued", "--follow")
	require.Eventually(t, func() bool { return notes.String() == "job job-queued is queued: its log follows once it starts\n" },
		10*time.Second, 5*time.Millisecond)

	job.start("step one\n")
	require.Eventually(t, func() bool { return followed.String() == "step one\n" }, 10*time.Second, 5*time.Millisecond)
	job.say("step two\n")
	job.end()
	select {
	case code := <-exited:
		require.Equal(t, 0, code)
	case <-time.After(10 * time.Second):
		t.Fatal("tau logs --follow outlived the job")
	}
	require.Equal(t, "step one\nstep two\n", followed.String())
}

func TestLogsNeverSpliceTwoRuns(t *testing.T) {
	x := newTester(t)
	x.login()
	x.f.mu.Lock()
	x.f.runLogs["job-live/2"] = "the rerun, from its own start\n"
	x.f.dropOnce, x.f.rerunOnDrop = true, true
	x.f.mu.Unlock()

	r := x.run("logs", "job-live", "--follow")
	require.NotZero(t, r.code)
	require.Equal(t, "step ", r.stdout, "nothing of the rerun follows the dropped run's bytes")
	require.Contains(t, r.stderr, "job job-live: the log of attempt 1 was not kept past byte 5")
	require.Equal(t, []string{"", "attempt=1&offset=5"}, x.f.logQueries, "the resume named its run")

	// And a job whose last run left no log has none to print, which is not an
	// empty one.
	x.f.mu.Lock()
	x.f.gone["job-2/2"] = true
	x.f.mu.Unlock()
	r = x.run("logs", "job-2")
	require.NotZero(t, r.code)
	require.Empty(t, r.stdout)
	require.Contains(t, r.stderr, "job job-2: the log of attempt 2 was not kept")
}

func TestLogsOfACancelledJobAreItsLastRuns(t *testing.T) {
	x := newTester(t)
	x.login()
	x.f.mu.Lock()
	x.f.jobs["job-cx"] = &buildv2.Job{Id: "job-cx", Status: buildv2.JobStatus_JobStatusCancelled, RepositoryKey: "github.com/acme/pay", Attempt: 2, Environment: "prod",
		Logs: map[string]string{"00000000000000000001": "bafy1"}}
	x.f.runLogs["job-cx/1"] = "the first run\n"
	x.f.runLogs["job-cx/2"] = "the second run, cancelled\n"
	x.f.mu.Unlock()

	require.Equal(t, "stored: the second run, cancelled\n", x.ok("logs", "job-cx"))
	require.Equal(t, "stored: the second run, cancelled\n", x.ok("logs", "job-cx", "--follow"))
	require.Equal(t, []string{"snapshot=true", ""}, x.f.logQueries)
}

func TestCancelAndRetry(t *testing.T) {
	x := newTester(t)
	x.login()
	require.Contains(t, x.run("cancel", "job-1").stderr, "pass --yes")
	require.Contains(t, x.ok("cancel", "job-1", "--yes"), "cancelled job-1")

	x.interactive = true
	x.prompter.answers = []string{"no"}
	require.Contains(t, x.run("cancel", "job-1").stderr, "not confirmed")
	x.prompter.answers = []string{"yes"}
	x.ok("cancel", "job-1")
	x.prompter.err = errors.New("interrupted")
	require.NotZero(t, x.run("cancel", "job-1").code)

	require.Contains(t, x.ok("retry", "job-1"), "queued job-1 again")
	require.Contains(t, x.run("retry", "busy").stderr, "503: busy")
	require.NotZero(t, x.run("retry").code)
	require.NotZero(t, x.run("cancel").code)
}

func TestDeployIsPushDriven(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")
	golden(t, "deploy", x.ok("deploy", "--dir", x.dir))
	require.Contains(t, x.ok("deploy", "--project", "proj-1", "--dir", t.TempDir()), "push the project's repositories")
	require.NotZero(t, x.run("deploy", "--dir", t.TempDir()).code)
}

func TestDeployWaitFollowsTheNextJob(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")

	// The push lands while the CLI is waiting.
	x.f.pushLater("proj-1", "job-3", "building\n")
	out := x.ok("deploy", "--dir", x.dir, "--wait", "--interval", "10ms")
	require.Contains(t, out, "job job-3 started")
	require.Contains(t, out, "building\n")

	r := x.run("deploy", "--dir", x.dir, "--wait", "--interval", "5ms", "--timeout", "20ms")
	require.Contains(t, r.stderr, "no job started")

	x.f.addJob("proj-1", "job-4", "")
	x.interactive = true
	x.f.pushLater("proj-1", "job-5", "x\n")
	x.ok("deploy", "--dir", x.dir, "--wait", "--interval", "10ms")
	require.NotZero(t, x.run("deploy", "--dir", x.dir, "--wait", "--interval", "5ms", "--timeout", "20ms").code)

	x.interactive = false
	x.f.pushLater("proj-1", "job-6", "")
	require.Contains(t, x.ok("deploy", "--dir", x.dir, "--wait", "--interval", "10ms", "-o", "json"), `"job": "job-6"`)

	require.NotZero(t, x.run("deploy", "--project", "proj-nope", "--wait").code)
}

func TestSecrets(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")

	require.Contains(t, x.run("secrets", "list", "--dir", x.dir).stderr, "--env is required")
	require.Contains(t, x.run("secrets", "list", "--dir", x.dir, "--env", "prod").stderr, "named in the project's configuration",
		"a name means nothing without the configuration that declares it")

	x.ok("init", "shop", "--dir", x.dir)
	for _, name := range []string{"db_url", "cert", "api_key"} {
		x.ok("new", "secret", name, "--dir", x.dir)
	}
	p, err := project.Open(x.dir)
	require.NoError(t, err)
	idOf := func(name string) string {
		id, err := p.IDOf("secret", name)
		require.NoError(t, err)
		return id
	}
	golden(t, "secrets-empty", x.ok("secrets", "list", "--dir", x.dir, "--env", "prod"))

	// THE VAULT KEEPS A VALUE UNDER THE SECRET'S ID, which is what a workload's grant
	// reads; the developer names it.
	x.ok("secrets", "set", "db_url", "postgres://x", "--dir", x.dir, "--env", "prod")
	require.Equal(t, []byte("postgres://x"), x.f.secrets["proj-1/prod"][idOf("db_url")])
	x.stdin = "line one\nline two\n"
	x.ok("secrets", "set", "cert", "--value-stdin", "--dir", x.dir, "--env", "prod")
	require.Equal(t, []byte("line one\nline two\n"), x.f.secrets["proj-1/prod"][idOf("cert")])
	require.NotContains(t, x.f.secrets["proj-1/prod"], "cert")

	// One the configuration does not declare is refused, naming it.
	require.Contains(t, x.run("secrets", "set", "DB_URL", "x", "--dir", x.dir, "--env", "prod").stderr, `declares no secret "DB_URL"`)

	golden(t, "secrets", x.ok("secrets", "list", "--dir", x.dir, "--env", "prod"))
	require.Contains(t, x.run("secrets", "unset", "cert", "--dir", x.dir, "--env", "prod").stderr, "pass --yes")
	x.ok("secrets", "unset", "cert", "--dir", x.dir, "--env", "prod", "--yes")
	require.NotContains(t, x.f.secrets["proj-1/prod"], idOf("cert"))

	// A secret the configuration stopped declaring is listed, and unset, by its id.
	orphan := idOf("db_url")
	require.NoError(t, os.Remove(filepath.Join(x.dir, "secret", "db_url.yaml")))
	require.Contains(t, x.ok("secrets", "list", "--dir", x.dir, "--env", "prod"), orphan)
	require.NotZero(t, x.run("secrets", "unset", "QmNotHeld", "--dir", x.dir, "--env", "prod", "--yes").code, "an id the vault does not hold is not removed")
	x.ok("secrets", "unset", orphan, "--dir", x.dir, "--env", "prod", "--yes")
	require.NotContains(t, x.f.secrets["proj-1/prod"], orphan)

	require.Contains(t, x.run("secrets", "set", "--dir", x.dir, "--env", "prod").stderr, "name is required")
	require.Contains(t, x.run("secrets", "set", "api_key", "--dir", x.dir, "--env", "prod").stderr, "a value is required")
	require.NotZero(t, x.run("secrets", "unset", "--dir", x.dir, "--env", "prod").code)
	require.NotZero(t, x.run("secrets", "list", "--dir", t.TempDir(), "--env", "prod").code)
	require.NotZero(t, x.run("secrets", "list", "--project", "proj-1", "--env", "prod", "--cloud", "::").code)

	// At a terminal, the environment and the value are asked for, the value hidden.
	x.interactive = true
	x.prompter.answers = []string{"prod", "s3cret"}
	x.ok("secrets", "set", "api_key", "--dir", x.dir)
	require.Equal(t, []byte("s3cret"), x.f.secrets["proj-1/prod"][idOf("api_key")])
	require.Equal(t, []string{"Environment", "Value of api_key"}, x.prompter.asked)
}
