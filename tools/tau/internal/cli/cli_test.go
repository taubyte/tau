package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/tools/tau/internal/config"
)

// ── login / profiles ─────────────────────────────────────────────────────────

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
	x.ok("login", "--profile", "staging", "--cloud", "https://staging.example.com", "--token-stdin", "--use=false")

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
	require.Contains(t, r.stderr, "--token or --token-stdin is required")

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

// At a terminal, login asks for what it was not given — the token hidden.
func TestLoginPromptsAtATerminal(t *testing.T) {
	x := newTester(t)
	x.interactive = true
	x.prompter.answers = []string{x.f.srv.URL, goodToken}
	x.ok("login")
	require.Equal(t, []string{"Cloud URL (the build frontend)", "Forge token"}, x.prompter.asked)
}

// ── project: init / new / validate / views ──────────────────────────────────

func TestInitValidateViews(t *testing.T) {
	x := newTester(t)
	out := x.ok("init", "shop", "--dir", x.dir)
	require.Contains(t, out, `created project "shop"`)
	require.FileExists(t, filepath.Join(x.dir, "project.yaml"))

	golden(t, "validate-starter", x.ok("validate", "--dir", x.dir))
	golden(t, "views-starter", x.ok("views", "--dir", x.dir))

	var report struct {
		Error    string `json:"error"`
		Findings []any  `json:"findings"`
	}
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

	r := x.run("new", "function", "bye", "--dir", x.dir, "--set", "source=services/bye")
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "--set execution.call=")
	require.NoFileExists(t, filepath.Join(x.dir, "function", "bye.yaml"))

	out := x.ok("new", "function", "bye", "--dir", x.dir,
		"--set", "source=services/bye", "--set", "execution.call=handle",
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

// At a terminal, new asks for the kind, the name and each field still owed,
// checking each answer against the field's rule.
func TestNewPromptsForWhatTheResourceOwes(t *testing.T) {
	x := newTester(t)
	x.ok("init", "shop", "--dir", x.dir)
	x.interactive = true
	x.prompter.answers = []string{"function", "bye", "handle", "10s", "16MB"}
	out := x.ok("new", "--dir", x.dir, "--set", "source=services/bye")
	require.Contains(t, out, "created function/bye")
	require.Equal(t, []string{"Kind", "Name", "execution.call", "execution.timeout", "execution.memory"}, x.prompter.asked)

	x.prompter.answers = []string{"function", "bad", "handle", "10s", "lots"}
	require.NotZero(t, x.run("new", "--dir", x.dir).code)

	x.prompter.err = errors.New("interrupted")
	require.NotZero(t, x.run("new", "--dir", x.dir).code)
	require.NotZero(t, x.run("new", "function", "--dir", x.dir).code)
	require.NotZero(t, x.run("new", "function", "y", "--dir", x.dir).code)
	require.NotZero(t, x.run("init", "--dir", t.TempDir()).code)
}

// ── link / status ────────────────────────────────────────────────────────────

func TestLinkCreatesTheProjectAndRemembersIt(t *testing.T) {
	x := newTester(t)
	x.login()
	x.ok("init", "shop", "--dir", x.dir)

	out := x.ok("link", "--dir", x.dir, "--config-repo", "https://github.com/acme/cfg.git", "--code-repo", "git@github.com:acme/pay.git")
	require.Contains(t, out, `linked "shop" as proj-shop`)
	require.Equal(t, []string{"shop|github.com/acme/cfg|github.com/acme/pay"}, x.f.created)

	l, err := config.ReadLink(x.dir)
	require.NoError(t, err)
	require.Equal(t, "proj-shop", l.ProjectID)

	golden(t, "status-linked-empty", strings.ReplaceAll(x.ok("status", "--dir", x.dir), x.f.srv.URL, "URL"))

	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", "github.com/acme/cfg").stderr, "--code-repo is required")
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", "nope", "--code-repo", "github.com/acme/pay").code)
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", "github.com/acme/cfg", "--code-repo", "::").code)
	require.Contains(t, x.run("link", "--dir", t.TempDir(), "--config-repo", "github.com/acme/cfg", "--code-repo", "github.com/acme/pay").stderr, "--name is required")

	x.interactive = true
	x.prompter.answers = []string{"github.com/acme/cfg", "github.com/acme/pay"}
	x.ok("link", "--dir", t.TempDir(), "--name", "other")
}

func TestLinkNeedsALogin(t *testing.T) {
	x := newTester(t)
	r := x.run("link", "--dir", x.dir, "--name", "shop", "--config-repo", "github.com/acme/cfg", "--code-repo", "github.com/acme/pay")
	require.Contains(t, r.stderr, "tau login")

	x.ok("login", "--cloud", x.f.srv.URL, "--token", "tok-bad")
	r = x.run("link", "--dir", x.dir, "--name", "shop", "--config-repo", "github.com/acme/cfg", "--code-repo", "github.com/acme/pay")
	require.Contains(t, r.stderr, "unauthorized")
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

// ── jobs / job / logs / cancel / retry ───────────────────────────────────────

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
	require.Equal(t, "step one\nstep two\n", x.ok("logs", "job-1"))
	require.Equal(t, "step two\n", x.ok("logs", "job-1", "--offset", "9"))

	// A dropped connection resumes at the byte offset reached, so nothing is
	// printed twice and nothing is lost.
	x.f.dropOnce = true
	require.Equal(t, "step one\nstep two\n", x.ok("logs", "job-1", "--follow"))

	// At a terminal, --follow draws the live view and reads the same bytes.
	x.interactive = true
	x.ok("logs", "job-1", "-f")

	require.NotZero(t, x.run("logs").code)
	require.Contains(t, x.run("logs", "nope").stderr, "not found")
	require.Contains(t, x.run("logs", "nope", "-f").stderr, "not found")
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

// ── deploy ───────────────────────────────────────────────────────────────────

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

// ── secrets ──────────────────────────────────────────────────────────────────

func TestSecrets(t *testing.T) {
	x := newTester(t)
	x.login()
	x.link("proj-1")

	require.Contains(t, x.run("secrets", "list", "--dir", x.dir).stderr, "--env is required")
	golden(t, "secrets-empty", x.ok("secrets", "list", "--dir", x.dir, "--env", "prod"))

	x.ok("secrets", "set", "DB_URL", "postgres://x", "--dir", x.dir, "--env", "prod")
	x.stdin = "line one\nline two\n"
	x.ok("secrets", "set", "CERT", "--value-stdin", "--dir", x.dir, "--env", "prod")
	require.Equal(t, []byte("line one\nline two\n"), x.f.secrets["proj-1/prod"]["CERT"])

	golden(t, "secrets", x.ok("secrets", "list", "--dir", x.dir, "--env", "prod"))
	require.Contains(t, x.run("secrets", "unset", "CERT", "--dir", x.dir, "--env", "prod").stderr, "pass --yes")
	x.ok("secrets", "unset", "CERT", "--dir", x.dir, "--env", "prod", "--yes")
	require.NotContains(t, x.f.secrets["proj-1/prod"], "CERT")

	require.Contains(t, x.run("secrets", "set", "--dir", x.dir, "--env", "prod").stderr, "name is required")
	require.Contains(t, x.run("secrets", "set", "X", "--dir", x.dir, "--env", "prod").stderr, "a value is required")
	require.NotZero(t, x.run("secrets", "unset", "--dir", x.dir, "--env", "prod").code)
	require.NotZero(t, x.run("secrets", "list", "--dir", t.TempDir(), "--env", "prod").code)
	require.NotZero(t, x.run("secrets", "list", "--project", "proj-1", "--env", "prod", "--cloud", "::").code)

	// At a terminal, the environment and the value are asked for, the value hidden.
	x.ok("init", "shop", "--dir", x.dir)
	x.interactive = true
	x.prompter.answers = []string{"prod", "s3cret"}
	x.ok("secrets", "set", "API_KEY", "--dir", x.dir)
	require.Equal(t, []byte("s3cret"), x.f.secrets["proj-1/prod"]["API_KEY"])
	require.Equal(t, []string{"Environment", "Value of API_KEY"}, x.prompter.asked)
}
