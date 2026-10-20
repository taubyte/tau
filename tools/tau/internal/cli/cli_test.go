package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/linkcreds"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/project"
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

// At a terminal, login asks for what it was not given — the password hidden.
func TestLoginPromptsAtATerminal(t *testing.T) {
	x := newTester(t)
	x.interactive = true
	x.prompter.answers = []string{x.f.srv.URL, "password", "root", "hunter2"}
	x.ok("login")
	require.Equal(t, []string{"Cloud URL (the build frontend)", "Sign in with", "User", "Password"}, x.prompter.asked)
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

// linkTester is a tester whose forge is pkg/forge/fake, so what `tau link`
// installs is recorded by a real driver.
func linkTester(t *testing.T) (*tester, *fake.Forge) {
	x := newTester(t)
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	x.forges = reg
	x.stdin = f.TokenString() + "\n"
	x.ok("login", "--cloud", x.f.srv.URL, "--sso", f.Host(), "--paste")
	x.stdin = ""
	x.ok("init", "shop", "--dir", x.dir)
	return x, f
}

func projectYAMLID(t *testing.T, dir string) string {
	t.Helper()
	p, err := project.Open(dir)
	require.NoError(t, err)
	require.NotEmpty(t, p.ID())
	return p.ID()
}

// `tau link` claims project.yaml's own id, stores the credentials it made on
// the cloud, and installs their public halves on the forge with the
// developer's token.
func TestLinkClaimsTheProjectsOwnIdAndInstallsItsCredentials(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	cfg, code := f.Keys().Admin, f.Keys().Deletable

	out := x.ok("link", "--dir", x.dir, "--config-repo", "https://"+cfg.String()+".git", "--code-repo", code.String())
	require.Contains(t, out, "linked "+cfg.String()+" as "+id)
	require.Equal(t, []string{id + "|" + cfg.String() + "|" + code.String()}, x.f.created, "the id is project.yaml's, never one the cloud mints")

	// The cloud got the private halves under the well-known names.
	names := x.f.secrets[id+"/"+linkcreds.Environment]
	require.Contains(t, names, linkcreds.HookSecret)
	require.Contains(t, names, linkcreds.DeployKey(cfg.String()))
	require.Contains(t, names, linkcreds.DeployKey(code.String()))
	require.Contains(t, string(names[linkcreds.DeployKey(code.String())]), "OPENSSH PRIVATE KEY")
	require.Len(t, names[linkcreds.HookSecret], forge.SecretSize)

	// The forge got a webhook and a read-only deploy key.
	require.NotEmpty(t, f.LastHookWire())
	require.True(t, f.LastDeployKeyReadOnly())

	l, err := config.ReadLink(x.dir)
	require.NoError(t, err)
	require.Equal(t, id, l.ProjectID)
	require.Equal(t, []string{code.String()}, l.Code)

	golden(t, "status-linked-empty", strings.ReplaceAll(x.ok("status", "--dir", x.dir), x.f.srv.URL, "URL"))

	var listed []map[string]any
	require.NoError(t, json.Unmarshal([]byte(x.ok("projects", "-o", "json")), &listed))
	require.Len(t, listed, 1)
	require.Contains(t, x.ok("projects"), id)
}

// AN ID SOMEBODY ELSE CLAIMED IS SHOWN AS THE CLOUD SAID IT, and nothing is
// stored or installed.
func TestLinkShowsAClaimedIdsRefusal(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	yaml, err := os.ReadFile(filepath.Join(x.dir, "project.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "project.yaml"), []byte(strings.Replace(string(yaml), id, takenID, 1)), 0o644))
	require.Equal(t, takenID, projectYAMLID(t, x.dir))

	r := x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "is already claimed by github.com/someone/else; generate a new id in project.yaml")
	require.Empty(t, x.f.secrets)
	require.Empty(t, f.LastHookWire(), "nothing reached the forge")
}

// --manual installs nothing and prints what to paste: the forge's wire form of
// the secret, which is what the cloud verifies against.
func TestLinkManualPrintsWhatToInstall(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)

	var out struct {
		ProjectID string `json:"project_id"`
		Install   []struct {
			Repository, Webhook, Secret string
			DeployKey                   string `json:"deploy_key"`
		} `json:"install"`
	}
	require.NoError(t, json.Unmarshal([]byte(x.ok("link", "--manual", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "-o", "json")), &out))
	require.Equal(t, id, out.ProjectID)
	require.Len(t, out.Install, 1)
	require.Equal(t, x.f.srv.URL+"/hook/"+id, out.Install[0].Webhook)
	require.True(t, strings.HasPrefix(out.Install[0].DeployKey, "ssh-ed25519 "))
	wire, err := forge.SchemeHubSHA256.Wire(forge.SecretFrom(x.f.secrets[id+"/"+linkcreds.Environment][linkcreds.HookSecret]))
	require.NoError(t, err)
	require.Equal(t, wire, out.Install[0].Secret)
	require.Empty(t, f.LastHookWire(), "--manual sends nothing to the forge")

	require.Contains(t, x.ok("link", "--manual", "--dir", x.dir, "--config-repo", f.Keys().Admin.String()), "Install these yourself")
}

func TestLinkRefusals(t *testing.T) {
	x, f := linkTester(t)
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", "nope").code)
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "--code-repo", "::").code)
	require.Contains(t, x.run("link", "--dir", t.TempDir(), "--config-repo", f.Keys().Admin.String()).stderr, "tau init")
	require.Contains(t, x.run("link", "--dir", x.dir).stderr, "--config-repo is required")

	// A repository the forge does not know, one the token may not administer,
	// or a forge this CLI has no driver for.
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Missing.String()).code)
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Writable.String()).stderr, "takes admin")
	r := x.run("link", "--dir", x.dir, "--config-repo", "codeberg.org/acme/cfg")
	require.Contains(t, r.stderr, "--manual")
	require.Empty(t, x.f.created)

	x.interactive = true
	x.prompter.answers = []string{f.Keys().Admin.String()}
	x.ok("link", "--dir", x.dir)
}

// A CLOUD THAT CANNOT STORE THE CREDENTIALS stops the link before anything is
// installed on the forge, and a forge this CLI cannot sign for cannot be
// installed by hand either.
func TestLinkStopsWhereItFails(t *testing.T) {
	x, f := linkTester(t)
	x.f.failSecrets = true
	r := x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.Contains(t, r.stderr, "storing the link's credentials")
	require.Empty(t, f.LastHookWire())
	x.f.failSecrets = false

	r = x.run("link", "--manual", "--dir", x.dir, "--config-repo", "codeberg.org/acme/cfg")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "codeberg.org")

	require.Len(t, x.f.created, 1, "the manual link to a forge it cannot sign for claimed nothing")
	require.Contains(t, x.ok("projects"), f.Keys().Admin.String(), "a claim whose credentials failed to store is still the cloud's")
}

func TestNoProjectsYet(t *testing.T) {
	x := newTester(t)
	x.login()
	require.Contains(t, x.ok("projects"), "no projects linked yet")
	require.Equal(t, "[]\n", x.ok("projects", "-o", "json"))
}

func TestLinkNeedsALogin(t *testing.T) {
	x := newTester(t)
	x.ok("init", "shop", "--dir", x.dir)
	r := x.run("link", "--dir", x.dir, "--config-repo", "github.com/acme/cfg")
	require.Contains(t, r.stderr, "tau login")

	t.Setenv(config.EnvToken, "tok-bad")
	r = x.run("link", "--cloud", x.f.srv.URL, "--manual", "--dir", x.dir, "--config-repo", "github.com/acme/cfg")
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

// hooksOn is what the fake forge holds on key: the hooks and the deploy keys.
func hooksOn(t *testing.T, x *tester, f *fake.Forge, key repokey.Key) ([]forge.InstalledPushHook, []forge.InstalledDeployKey) {
	t.Helper()
	s, err := x.forges.For(key, f.Token())
	require.NoError(t, err)
	hooks, err := s.ListPushHooks(context.Background(), key)
	require.NoError(t, err)
	keys, err := s.ListDeployKeys(context.Background(), key)
	require.NoError(t, err)
	return hooks, keys
}

// A SECOND LINK FINDS THE FIRST ONE'S HOOK and asks before replacing it; it
// never installs a duplicate that the rotated secret would leave dead.
func TestRelinkAsksAboutTheHookAlreadyInstalled(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	cfg := f.Keys().Admin
	x.ok("link", "--dir", x.dir, "--config-repo", cfg.String())
	first, firstKeys := hooksOn(t, x, f, cfg)
	require.Len(t, first, 1)
	require.Len(t, firstKeys, 1)
	require.Equal(t, x.f.srv.URL+"/hook/"+id, first[0].Callback)
	secret := func() string { return string(x.f.secrets[id+"/"+linkcreds.Environment][linkcreds.HookSecret]) }
	stored := secret()

	// Not at a terminal, and no flag: refused, naming both flags, and nothing
	// moved.
	r := x.run("link", "--dir", x.dir, "--config-repo", cfg.String())
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "--replace-hook")
	require.Contains(t, r.stderr, "--keep-hook")
	require.Equal(t, stored, secret())

	// At a terminal, no: the hook and the credentials stay.
	x.interactive = true
	x.prompter.answers = []string{"no"}
	require.Contains(t, x.ok("link", "--dir", x.dir, "--config-repo", cfg.String()), "kept the webhook")
	require.Contains(t, x.prompter.asked[len(x.prompter.asked)-1], cfg.String())
	hooks, keys := hooksOn(t, x, f, cfg)
	require.Equal(t, first, hooks)
	require.Equal(t, firstKeys, keys)
	require.Equal(t, stored, secret())

	// At a terminal, yes: the old hook and deploy key are gone, one new of
	// each is installed, and the secret rotated.
	x.prompter.answers = []string{"yes"}
	require.Contains(t, x.ok("link", "--dir", x.dir, "--config-repo", cfg.String()), "deleted this cloud's previous webhook")
	hooks, keys = hooksOn(t, x, f, cfg)
	require.Len(t, hooks, 1)
	require.Len(t, keys, 1)
	require.NotEqual(t, first[0].Handle, hooks[0].Handle)
	require.NotEqual(t, firstKeys[0].Handle, keys[0].Handle)
	require.NotEqual(t, stored, secret())

	// A prompt that fails stops the link.
	x.prompter.err = errors.New("interrupted")
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", cfg.String()).stderr, "interrupted")
	x.prompter.err = nil
	x.interactive = false

	// The flags decide without asking.
	before, _ := hooksOn(t, x, f, cfg)
	x.ok("link", "--dir", x.dir, "--config-repo", cfg.String(), "--keep-hook")
	after, _ := hooksOn(t, x, f, cfg)
	require.Equal(t, before, after)
	x.ok("link", "--dir", x.dir, "--config-repo", cfg.String(), "--replace-hook")
	after, keys = hooksOn(t, x, f, cfg)
	require.Len(t, after, 1)
	require.Len(t, keys, 1)
	require.NotEqual(t, before[0].Handle, after[0].Handle)
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", cfg.String(), "--keep-hook", "--replace-hook").code)

	// Keeping cannot give a newly added repository a hook: the secret it would
	// need is the cloud's alone.
	r = x.run("link", "--dir", x.dir, "--config-repo", cfg.String(), "--code-repo", f.Keys().Deletable.String(), "--keep-hook")
	require.Contains(t, r.stderr, f.Keys().Deletable.String()+" cannot be given a hook")
	x.ok("link", "--dir", x.dir, "--config-repo", cfg.String(), "--code-repo", f.Keys().Deletable.String(), "--replace-hook")
	code, codeKeys := hooksOn(t, x, f, f.Keys().Deletable)
	require.Len(t, code, 1)
	require.Len(t, codeKeys, 1)
}

// A forge that cannot answer the questions a relink asks stops it.
func TestRelinkStopsWhenTheForgeCannotAnswer(t *testing.T) {
	x, f := linkTester(t)
	cfg := f.Keys().Admin
	x.ok("link", "--dir", x.dir, "--config-repo", cfg.String())
	f.Break()
	defer f.Unbreak()
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", cfg.String(), "--replace-hook").code)
}

// stuckForge is a forge whose removals, or whose deploy-key listing, fail: the
// one shape the fake forge cannot take, and the one a replacing link must stop
// on rather than install beside what it could not delete.
type stuckForge struct {
	host, callback, title string
	failHook, failList    bool
}

func (d *stuckForge) Host() string                  { return d.host }
func (d *stuckForge) Addressable(repokey.Key) error { return nil }
func (d *stuckForge) Bind(forge.Credential) (forge.Client, error) {
	return d, nil
}
func (d *stuckForge) Scheme() forge.Scheme { return forge.SchemeHubSHA256 }
func (d *stuckForge) Push(forge.VerifiedBody, http.Header) (forge.PushClaim, error) {
	return forge.PushClaim{}, forge.ErrNotAPush
}
func (d *stuckForge) Whoami(context.Context) (forge.Identity, error) {
	return forge.Identity{Login: "me"}, nil
}
func (d *stuckForge) Access(context.Context, repokey.Key) (forge.Access, error) {
	return forge.AccessAdmin, nil
}
func (d *stuckForge) Repository(_ context.Context, k repokey.Key) (forge.Repository, error) {
	return forge.Repository{Key: k}, nil
}
func (d *stuckForge) RegisterPushHook(context.Context, repokey.Key, *url.URL, string) (forge.Handle, error) {
	return "h-new", nil
}
func (d *stuckForge) RemovePushHook(context.Context, repokey.Key, forge.Handle) error {
	if d.failHook {
		return errors.New("stuck hook")
	}
	return nil
}
func (d *stuckForge) ListPushHooks(context.Context, repokey.Key) ([]forge.InstalledPushHook, error) {
	return []forge.InstalledPushHook{{Handle: "h-old", Callback: d.callback}}, nil
}
func (d *stuckForge) InstallDeployKey(context.Context, repokey.Key, string, string) (forge.Handle, error) {
	return "k-new", nil
}
func (d *stuckForge) RemoveDeployKey(context.Context, repokey.Key, forge.Handle) error {
	return errors.New("stuck key")
}
func (d *stuckForge) ListDeployKeys(context.Context, repokey.Key) ([]forge.InstalledDeployKey, error) {
	if d.failList {
		return nil, errors.New("stuck listing")
	}
	return []forge.InstalledDeployKey{{Handle: "k-other", Title: "someone else's"}, {Handle: "k-old", Title: d.title}}, nil
}

func TestReplacingStopsOnWhatItCouldNotDelete(t *testing.T) {
	for want, d := range map[string]*stuckForge{
		"deleting the previous webhook":    {failHook: true},
		"reading the deploy keys":          {failList: true},
		"deleting the previous deploy key": {},
	} {
		x := newTester(t)
		d.host = "forge.example"
		d.callback = "" // set once the cloud's URL is known
		reg, err := forge.New(forge.Policy{AllowInsecureCallback: true}, forge.Binding{Host: d.host, Driver: d})
		require.NoError(t, err)
		x.forges = reg
		x.stdin = forgeToken + "\n"
		x.ok("login", "--cloud", x.f.srv.URL, "--sso", d.host, "--paste")
		x.stdin = ""
		x.ok("init", "shop", "--dir", x.dir)
		d.callback = x.f.srv.URL + "/hook/" + projectYAMLID(t, x.dir)
		d.title = "tau " + x.f.srv.URL
		r := x.run("link", "--dir", x.dir, "--config-repo", d.host+"/acme/cfg", "--replace-hook")
		require.Contains(t, r.stderr, want)
	}
}
