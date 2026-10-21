package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

// linkTester is a tester signed in through a fake forge, whose credential the
// sign-in handed back, with a project in its directory.
func linkTester(t *testing.T) (*tester, *fake.Forge) {
	return linkTesterOn(t, fake.New(fake.GitHubLike))
}

func linkTesterOn(t *testing.T, f *fake.Forge) (*tester, *fake.Forge) {
	x := newTester(t)
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

func hookOf(id string) string { return "https://build-frontend.cloud.example/hook/" + id }

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

// THE CLI DOES THE FORGE WORK with its own credential: each repository gets
// its own secret and deploy key, installed at the forge from here, and the
// cloud is only handed the private halves and the registration.
func TestLinkInstallsEverythingFromTheClient(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	cfg, code := f.Keys().Admin, f.Keys().Deletable

	out := x.ok("link", "--dir", x.dir, "--config-repo", "https://"+cfg.String()+".git", "--code-repo", code.String())
	require.Contains(t, out, "linked "+cfg.String()+" as "+id)
	require.Contains(t, out, "installed a webhook and a read-only deploy key on "+code.String())
	require.Equal(t, []string{id + "|" + cfg.String() + "|" + code.String()}, x.f.created, "the id is project.yaml's")

	names := x.f.secrets[id+"/"+linkcreds.Environment]
	require.Len(t, names, 4)
	for _, k := range []repokey.Key{cfg, code} {
		require.Len(t, names[linkcreds.HookSecret(k.String())], forge.SecretSize)
		require.Contains(t, string(names[linkcreds.DeployKey(k.String())]), "OPENSSH PRIVATE KEY")
		hooks, keys := hooksOn(t, x, f, k)
		require.Len(t, hooks, 1)
		require.Equal(t, hookOf(id), hooks[0].Callback, "aimed at the cloud's own name for its frontend")
		require.Len(t, keys, 1)
		require.Equal(t, "tau build-frontend.cloud.example", keys[0].Title)
	}
	require.NotEqual(t, names[linkcreds.HookSecret(cfg.String())], names[linkcreds.HookSecret(code.String())], "one secret per repository")
	require.True(t, f.LastDeployKeyReadOnly())

	for _, c := range x.f.calls {
		require.NotContains(t, c, "/link/")
		require.NotContains(t, c, "/forges/")
	}

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

// AN ORGANIZATION'S REPOSITORIES, under any name, are created and linked like
// anyone's: the namespace is the key's, never the signed-in login's.
func TestLinkCreatesOrgOwnedRepositories(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	cfg := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "shop-config"}
	code := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "billing.service"}

	r := x.run("link", "--dir", x.dir, "--config-repo", cfg.String())
	require.NotZero(t, r.code, "a repository that is not there is not created unasked")
	require.Contains(t, r.stderr, "reading "+cfg.String())

	out := x.ok("link", "--create", "--dir", x.dir, "--config-repo", cfg.String(), "--code-repo", code.String())
	require.Contains(t, out, "created "+cfg.String())
	require.Contains(t, out, "linked "+cfg.String()+" as "+id)
	require.NotEqual(t, "fake-user", cfg.Owner)
	hooks, _ := hooksOn(t, x, f, code)
	require.Len(t, hooks, 1)

	// Somewhere this credential may not create is the forge's refusal.
	r = x.run("link", "--create", "--dir", x.dir, "--config-repo", f.Keys().Uncreatable.String())
	require.Contains(t, r.stderr, "creating "+f.Keys().Uncreatable.String())
}

// A NESTED GROUP is a namespace like any other.
func TestLinkANestedGitLabGroup(t *testing.T) {
	x, f := linkTesterOn(t, fake.New(fake.GitLabLike))
	id := projectYAMLID(t, x.dir)
	deep := f.Keys().Deep
	fresh := repokey.Key{Host: f.Host(), Owner: deep.Owner, Name: "fresh"}
	require.Contains(t, deep.Owner, "/")

	out := x.ok("link", "--create", "--dir", x.dir, "--config-repo", deep.String(), "--code-repo", fresh.String())
	require.Contains(t, out, "created "+fresh.String())
	require.Equal(t, []string{id + "|" + deep.String() + "|" + fresh.String()}, x.f.created)
	hooks, _ := hooksOn(t, x, f, deep)
	require.Equal(t, hookOf(id), hooks[0].Callback)
}

// AN ID SOMEBODY ELSE CLAIMED IS SHOWN AS THE CLOUD SAID IT, and nothing is
// stored or installed.
func TestLinkShowsAClaimedIdsRefusal(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	yaml, err := os.ReadFile(filepath.Join(x.dir, "project.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "project.yaml"), []byte(strings.Replace(string(yaml), id, takenID, 1)), 0o644))

	r := x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "generate a new id in project.yaml")
	require.Empty(t, x.f.secrets)
	require.Empty(t, f.LastHookWire(), "nothing reached the forge")
}

// --manual installs nothing and prints, per repository, the forge's wire form
// of that repository's secret.
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
	cfg := f.Keys().Admin
	require.NoError(t, json.Unmarshal([]byte(x.ok("link", "--manual", "--dir", x.dir, "--config-repo", cfg.String(), "-o", "json")), &out))
	require.Equal(t, id, out.ProjectID)
	require.Len(t, out.Install, 1)
	require.Equal(t, hookOf(id), out.Install[0].Webhook)
	require.True(t, strings.HasPrefix(out.Install[0].DeployKey, "ssh-ed25519 "))
	wire, err := forge.SchemeHubSHA256.Wire(forge.SecretFrom(x.f.secrets[id+"/"+linkcreds.Environment][linkcreds.HookSecret(cfg.String())]))
	require.NoError(t, err)
	require.Equal(t, wire, out.Install[0].Secret)
	require.Empty(t, f.LastHookWire(), "--manual sends nothing to the forge")

	require.Contains(t, x.ok("link", "--manual", "--dir", x.dir, "--config-repo", cfg.String()), "Install these yourself")
	require.Contains(t, x.run("link", "--manual", "--create", "--dir", x.dir, "--config-repo", cfg.String()).stderr, "--manual touches no forge")
}

func TestLinkRefusals(t *testing.T) {
	x, f := linkTester(t)
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", "nope").code)
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "--code-repo", "::").code)
	require.Contains(t, x.run("link", "--dir", t.TempDir(), "--config-repo", f.Keys().Admin.String()).stderr, "tau init")
	require.Contains(t, x.run("link", "--dir", x.dir).stderr, "--config-repo is required")

	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().NoAccess.String()).code)
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Writable.String()).stderr, "takes admin")
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", "codeberg.org/acme/cfg").code)
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "--callback", "ftp://x").stderr, "not an http(s) URL")

	f.Break()
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String()).stderr, "reading")
	f.Unbreak()
	require.Empty(t, x.f.created)

	x.interactive = true
	x.prompter.answers = []string{f.Keys().Admin.String()}
	x.ok("link", "--dir", x.dir)
}

// THE CLOUD'S NAME decides the callback; a cloud that has none needs one
// named, which also serves a development cloud reached over http.
func TestLinkCallback(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	x.f.fqdn = ""
	r := x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.Contains(t, r.stderr, "--callback")

	x.ok("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "--callback", "http://127.0.0.1:9/hook/"+id)
	hooks, _ := hooksOn(t, x, f, f.Keys().Admin)
	require.Equal(t, "http://127.0.0.1:9/hook/"+id, hooks[0].Callback)
}

// A SIGN-IN THAT HANDED BACK NO FORGE CREDENTIAL cannot act at the forge, and
// says how to get one.
func TestLinkNeedsAForgeCredential(t *testing.T) {
	x := newTester(t)
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	x.forges = reg
	x.ok("init", "shop", "--dir", x.dir)
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String()).stderr, "tau login")

	x.login()
	r := x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.Contains(t, r.stderr, "tau forge connect "+f.Host())

	t.Setenv(config.EnvToken, "tok-bad")
	r = x.run("link", "--manual", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.Contains(t, r.stderr, "unauthorized")
}

// AN EXPIRED FORGE CREDENTIAL is renewed through the cloud and kept; one
// that cannot be renewed says to sign in again.
func TestLinkRenewsAnExpiredForgeCredential(t *testing.T) {
	x := newTester(t)
	f := fake.New(fake.GitHubLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	x.forges = reg
	x.f.auth.forgeExpires = 1
	x.stdin = f.TokenString() + "\n"
	x.ok("login", "--cloud", x.f.srv.URL, "--sso", f.Host(), "--paste")
	x.ok("init", "shop", "--dir", x.dir)

	x.ok("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.Equal(t, 1, x.f.auth.renewed)
	got := x.profile().Forges[f.Host()]
	require.Greater(t, got.Expires, int64(1))
	require.Equal(t, "forge-refresh", got.Refresh, "a renewal without a new refresh token keeps the old one")

	cfg := loadTestConfig(t)
	p := cfg.Profiles[cfg.Current]
	tok := p.Forges[f.Host()]
	tok.Expires, tok.Refresh = 1, "revoked"
	p.Forges[f.Host()] = tok
	cfg.Profiles[cfg.Current] = p
	require.NoError(t, cfg.Save())
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "--keep-hook").stderr, "renewing your "+f.Host()+" credential")

	tok.Refresh = ""
	p.Forges[f.Host()] = tok
	require.NoError(t, cfg.Save())
	require.Contains(t, x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String(), "--keep-hook").stderr, "cannot be renewed")
}

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	path, err := config.Path()
	require.NoError(t, err)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	return cfg
}

// A SECOND LINK FINDS THE FIRST ONE'S HOOK and asks before replacing it.
func TestRelinkAsksAboutTheHookAlreadyInstalled(t *testing.T) {
	x, f := linkTester(t)
	id := projectYAMLID(t, x.dir)
	cfg, code := f.Keys().Admin, f.Keys().Deletable
	x.ok("link", "--dir", x.dir, "--config-repo", cfg.String())
	first, firstKeys := hooksOn(t, x, f, cfg)
	require.Len(t, first, 1)
	require.Len(t, firstKeys, 1)
	secret := x.f.secrets[id+"/"+linkcreds.Environment][linkcreds.HookSecret(cfg.String())]

	r := x.run("link", "--dir", x.dir, "--config-repo", cfg.String())
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "--replace-hook")

	// Keeping leaves that repository alone and still wires a new one.
	out := x.ok("link", "--keep-hook", "--dir", x.dir, "--config-repo", cfg.String(), "--code-repo", code.String())
	require.Contains(t, out, "kept the webhooks")
	again, _ := hooksOn(t, x, f, cfg)
	require.Equal(t, first, again)
	require.Equal(t, secret, x.f.secrets[id+"/"+linkcreds.Environment][linkcreds.HookSecret(cfg.String())])
	codeHooks, _ := hooksOn(t, x, f, code)
	require.Len(t, codeHooks, 1)

	out = x.ok("link", "--replace-hook", "--dir", x.dir, "--config-repo", cfg.String(), "--code-repo", code.String())
	require.Contains(t, out, "deleted this cloud's previous webhook")
	after, afterKeys := hooksOn(t, x, f, cfg)
	require.Len(t, after, 1)
	require.Len(t, afterKeys, 1)
	require.NotEqual(t, first[0].Handle, after[0].Handle)
	require.NotEqual(t, firstKeys[0].Handle, afterKeys[0].Handle)
	require.NotEqual(t, secret, x.f.secrets[id+"/"+linkcreds.Environment][linkcreds.HookSecret(cfg.String())])

	x.interactive = true
	x.prompter.answers = []string{"no"}
	require.Contains(t, x.ok("link", "--dir", x.dir, "--config-repo", cfg.String()), "kept the webhooks")
	x.prompter.answers = []string{"yes"}
	require.Contains(t, x.ok("link", "--dir", x.dir, "--config-repo", cfg.String()), "deleted this cloud's previous webhook")
	x.prompter.err = context.Canceled
	require.NotZero(t, x.run("link", "--dir", x.dir, "--config-repo", cfg.String()).code)
}

// A CLOUD THAT CANNOT STORE THE CREDENTIALS stops the link before anything is
// installed on the forge.
func TestLinkStopsWhereItFails(t *testing.T) {
	x, f := linkTester(t)
	x.f.failSecrets = true
	r := x.run("link", "--dir", x.dir, "--config-repo", f.Keys().Admin.String())
	require.Contains(t, r.stderr, "storing the link's credentials")
	require.Empty(t, f.LastHookWire())
}

func TestNoProjectsYet(t *testing.T) {
	x := newTester(t)
	x.login()
	require.Contains(t, x.ok("projects"), "no projects linked yet")
	require.Equal(t, "[]\n", x.ok("projects", "-o", "json"))
}

// --push makes the directory the configuration repository's first commit,
// pushed over https with the forge credential, which lands nowhere on disk.
func TestLinkPushesTheProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
	served, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { served.Close() })
	x, f := linkTesterOn(t, served.Forge)
	cfg := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "shop"}

	var out linkOutput
	require.NoError(t, json.Unmarshal([]byte(x.ok("link", "--create", "--push", "--dir", x.dir, "--config-repo", cfg.String(), "-o", "json")), &out))
	require.Len(t, out.Pushed, 40)

	ls := exec.Command("git", "-c", "http.extraHeader=Authorization: "+basicAuth("x-access-token", f.TokenString()), "ls-remote", "http://"+cfg.String()+".git", "refs/heads/main")
	ls.Dir = t.TempDir()
	got, err := ls.CombinedOutput()
	require.NoError(t, err, string(got))
	require.True(t, strings.HasPrefix(string(got), out.Pushed))

	gitconfig, err := os.ReadFile(filepath.Join(x.dir, ".git", "config"))
	require.NoError(t, err)
	require.NotContains(t, string(gitconfig), f.TokenString())

	// An existing repository is pushed as it stands.
	require.NoError(t, os.WriteFile(filepath.Join(x.dir, "NOTES"), []byte("x"), 0o644))
	g := gitIn{dir: x.dir}
	_, err = g.run(context.Background(), "add", "NOTES")
	require.NoError(t, err)
	_, err = g.run(context.Background(), "commit", "-q", "-m", "notes")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(x.ok("link", "--push", "--keep-hook", "--dir", x.dir, "-o", "json")), &out))
	head, err := g.run(context.Background(), "rev-parse", "HEAD")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(head), out.Pushed)

	_, err = g.run(context.Background(), "no-such-command")
	require.Error(t, err)
}
