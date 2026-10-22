//go:build dreaming

// Package e2e runs the CLI against a real v2 fleet: a dream universe of two
// nodes running the build frontend (the public role) and a third running every
// private service they reach — auth, the build backend, secrets, register.
// `tau link` works at the fake forge itself, with the credential the forge
// sign-in handed back; the fleet only verifies deliveries with what the link
// stored and clones over the deploy key it installed.
package e2e

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/linkcreds"
	secretsv2 "github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2"
	"github.com/taubyte/tau/services/auth"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/tools/tau/internal/cli"
	"github.com/taubyte/tau/tools/tau/internal/config"
	"github.com/taubyte/tau/tools/tau/internal/project"
)

type result struct {
	code           int
	stdout, stderr string
}

// forges is what `tau link` installs on: the same fake forge the fleet
// verifies against.
var forges *forge.Registry

func tau(args ...string) result { return tauIn("", args...) }

// tauIn runs the CLI with stdin.
func tauIn(stdin string, args ...string) result {
	var out, errs bytes.Buffer
	code := cli.Run(cli.Env{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errs, HTTP: http.DefaultClient, Forges: forges}, args)
	return result{code, out.String(), errs.String()}
}

func ok(t *testing.T, args ...string) string {
	t.Helper()
	r := tau(args...)
	require.Equal(t, 0, r.code, "tau %s\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.stdout, r.stderr)
	return r.stdout
}

// e2eCloud is the fleet's cloud fqdn.
const e2eCloud = "tau.e2e.test"

// fleet is a dream universe of three: index 0 runs every private service,
// the others only a build frontend. The fake forge serves git over https and
// ssh, and every node pins its ssh host key, so a build confirms and clones
// over the deploy key the CLI installed.
func fleet(t *testing.T, f *fake.Served) (front, otherFront string) {
	t.Helper()
	var err error
	forges, err = f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	t.Cleanup(func() { forges = nil })
	hash, err := auth.HashAdminPassword(adminPassword)
	require.NoError(t, err)
	adminSecret, err := secrets.Sealed(secrets.Ref{Scope: secrets.Cloud(auth.VaultService), Name: auth.AdminSecret}, []byte(hash), time.Now())
	require.NoError(t, err)

	u, err := dream.Start(t.Context(), t.Name(), dream.Config{
		Copies:  3,
		Options: []node.Option{node.WithForges(forges)},
		Secrets: []*secretsv2.InitSecret{adminSecret},
		Configure: func(index int, c *node.Config) {
			c.Gateway.CloudFqdn = e2eCloud
			c.Forges = []node.Forge{{Host: f.Host(), Driver: node.ForgeGitHub, HostKeys: []string{f.HostKey()}}}
			c.Auth.Admin = auth.Admin{Name: "root"}
			if index > 0 {
				c.Services = []string{node.Discovery, node.BuildFrontend}
				c.BuildFrontend.Listen = "127.0.0.1:0"
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	addr := func(i int) string {
		n, found := dream.Node(u.Pids()[i])
		require.True(t, found)
		a := n.BuildFrontendAddr()
		require.NotNil(t, a)
		return "http://" + a.String()
	}
	return addr(1), addr(2)
}

const adminPassword = "correct horse battery"

func gitEnv(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
}

// THE CLIENT DOES THE FORGE WORK, THE CLOUD ONLY VERIFIES AND CLONES. An
// organization's repositories, named with no prefix of any kind, are created
// by the CLI with its own forge credential, wired, pushed, and built; the
// project is then shared with a second tau user by a grant, and unshared.
func TestTheCLIAgainstARealFleet(t *testing.T) {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")
	gitEnv(t)

	f, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	cloud, otherCloud := fleet(t, f)

	// The forge sign-in hands its credential back, and the CLI keeps it.
	var login result
	require.Eventually(t, func() bool {
		login = tauIn(f.TokenString()+"\n", "login", "--profile", "owner", "--cloud", cloud, "--sso", f.Host(), "--paste")
		return login.code == 0
	}, 2*time.Minute, time.Second, "login never succeeded: %s", login.stderr)
	path, err := config.Path()
	require.NoError(t, err)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, f.TokenString(), cfg.Profiles["owner"].Forges[f.Host()].Token)

	dir := t.TempDir()
	ok(t, "init", "shop", "--dir", dir)
	require.Contains(t, ok(t, "validate", "--dir", dir), "✓ compiles")

	configRepo := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "shop"}
	codeRepo := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "payments"}
	require.NotEqual(t, "fake-user", configRepo.Owner, "owned by an organization, not the login")

	var link result
	require.Eventually(t, func() bool {
		link = tau("link", "--profile", "owner", "--create", "--dir", dir, "--config-repo", configRepo.String(), "-o", "json")
		return link.code == 0
	}, 2*time.Minute, time.Second, "link never succeeded: %s", link.stderr)
	var linked struct {
		config.Link
		Pushed string `json:"pushed"`
	}
	require.NoError(t, json.Unmarshal([]byte(link.stdout), &linked))
	require.NotEmpty(t, linked.ProjectID)
	wire := f.LastHookWire()
	hooks := installed(t, f.Forge, configRepo)
	require.Len(t, hooks, 1)
	require.Equal(t, "https://"+node.BuildFrontend+"."+e2eCloud+"/hook/"+linked.ProjectID, hooks[0].Callback)
	require.True(t, f.LastDeployKeyReadOnly())

	// Relinking keeps the config repository's hook, creates and wires the
	// code repository, and pushes the project.
	require.Eventually(t, func() bool {
		link = tau("link", "--profile", "owner", "--create", "--push", "--keep-hook", "--dir", dir, "--config-repo", configRepo.String(), "--code-repo", codeRepo.String(), "-o", "json")
		if link.code != 0 {
			t.Log(link.stderr)
		}
		return link.code == 0
	}, 2*time.Minute, time.Second, "relink never succeeded: %s", link.stderr)
	require.NoError(t, json.Unmarshal([]byte(link.stdout), &linked))
	require.Len(t, linked.Pushed, 40)
	require.Equal(t, []string{codeRepo.String()}, linked.Code)
	require.Len(t, installed(t, f.Forge, configRepo), 1)
	require.Len(t, installed(t, f.Forge, codeRepo), 1)

	var names []string
	require.NoError(t, json.Unmarshal([]byte(ok(t, "secrets", "list", "--profile", "owner", "--dir", dir, "--env", linkcreds.Environment, "-o", "json")), &names))
	require.ElementsMatch(t, []string{
		linkcreds.HookSecret(configRepo.String()), linkcreds.DeployKey(configRepo.String()),
		linkcreds.HookSecret(codeRepo.String()), linkcreds.DeployKey(codeRepo.String()),
	}, names)

	// THE PUSH IS VERIFIED BY THE REPOSITORY'S OWN SECRET, and confirmed at
	// the forge over the deploy key before it is queued: a commit the branch
	// does not hold builds nothing.
	hook := cloud + "/hook/" + linked.ProjectID
	require.Equal(t, http.StatusNotFound, deliver(t, f.Forge, hook, configRepo, "not-the-secret", linked.Pushed).StatusCode)
	require.NotEqual(t, http.StatusAccepted, deliver(t, f.Forge, hook, configRepo, wire, strings.Repeat("a", 40)).StatusCode)
	require.Eventually(t, func() bool {
		return deliver(t, f.Forge, hook, configRepo, wire, linked.Pushed).StatusCode == http.StatusAccepted
	}, time.Minute, time.Second, "the pushed commit was never confirmed")
	require.Equal(t, http.StatusAccepted, deliver(t, f.Forge, otherCloud+"/hook/"+linked.ProjectID, configRepo, wire, linked.Pushed).StatusCode)

	var jobs []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	require.Eventually(t, func() bool {
		jobs = nil
		return json.Unmarshal([]byte(ok(t, "jobs", "--profile", "owner", "--dir", dir, "-o", "json")), &jobs) == nil && len(jobs) == 1
	}, time.Minute, time.Second, "the verified push queued a job")
	require.Eventually(t, func() bool {
		jobs = nil
		return json.Unmarshal([]byte(ok(t, "jobs", "--profile", "owner", "--dir", dir, "-o", "json")), &jobs) == nil && len(jobs) == 1 && jobs[0].Status == "success"
	}, 3*time.Minute, 2*time.Second, "the build cloned over the deploy key and succeeded: %v", jobs)

	// A SECOND TAU USER sees nothing until granted, then reads; revoked, the
	// project is a 404 again.
	r := tauIn(adminPassword+"\n", "login", "--profile", "admin", "--cloud", cloud, "--name", "root", "--password-stdin")
	require.Equal(t, 0, r.code, r.stderr)
	var bob struct {
		ID string `json:"id"`
	}
	r = tauIn("bobs-password-1\n", "users", "create", "bob", "--profile", "admin", "--login", "--password-stdin", "-o", "json")
	require.Equal(t, 0, r.code, r.stderr)
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &bob))
	r = tauIn("bobs-password-1\n", "login", "--profile", "bob", "--cloud", cloud, "--name", "bob", "--password-stdin")
	require.Equal(t, 0, r.code, r.stderr)

	// The same id from another tau user is refused by name, whatever the
	// repository.
	other := t.TempDir()
	require.NoError(t, copyProject(dir, other))
	require.NoError(t, os.RemoveAll(filepath.Join(other, ".git")))
	r = tau("link", "--profile", "bob", "--dir", other, "--config-repo", f.Keys().Admin.String(), "--manual", "--callback", "https://x.example/hook")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "already claimed")

	r = tau("jobs", "--profile", "bob", "--project", linked.ProjectID)
	require.Contains(t, r.stderr, "not found")
	require.Equal(t, "[]\n", ok(t, "projects", "--profile", "bob", "-o", "json"))

	var grant struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(ok(t, "grants", "add", bob.ID, "viewer", "--profile", "owner", "--dir", dir, "-o", "json")), &grant))
	require.Contains(t, tau("grants", "list", "--profile", "bob", "--project", linked.ProjectID).stderr, "not found", "a viewer does not see the grants")
	require.Eventually(t, func() bool {
		r := tau("jobs", "--profile", "bob", "--project", linked.ProjectID, "-o", "json")
		return r.code == 0 && strings.Contains(r.stdout, jobs[0].ID)
	}, time.Minute, time.Second, "the grant never let bob read the jobs")
	require.Contains(t, ok(t, "projects", "--profile", "bob"), linked.ProjectID)
	require.NotZero(t, tau("secrets", "set", "X", "y", "--profile", "bob", "--project", linked.ProjectID, "--env", "prod").code, "a viewer manages no secrets")

	ok(t, "grants", "revoke", grant.ID, "--profile", "owner", "--dir", dir, "--yes")
	require.Eventually(t, func() bool {
		return strings.Contains(tau("jobs", "--profile", "bob", "--project", linked.ProjectID).stderr, "not found")
	}, time.Minute, time.Second, "the revoked grant still let bob in")

	// Secrets, owner only.
	require.Eventually(t, func() bool {
		return tau("secrets", "set", "DB_URL", "postgres://x", "--profile", "owner", "--dir", dir, "--env", "prod").code == 0
	}, time.Minute, time.Second)
	ok(t, "secrets", "unset", "DB_URL", "--profile", "owner", "--dir", dir, "--env", "prod", "--yes")
	require.Contains(t, ok(t, "status", "--profile", "owner", "--dir", dir), linked.ProjectID)

	t.Setenv(config.EnvToken, f.BadTokenString())
	r = tau("jobs", "--dir", dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "unauthorized")
}

// deliver posts a push for repo to hook, signed the way a hub-sha256 forge
// signs: an HMAC of the body keyed with the wire secret it was given.
func deliver(t *testing.T, f *fake.Forge, hook string, repo repokey.Key, wire, commit string) *http.Response {
	t.Helper()
	// The fake builds the payload; it is re-signed with the installed wire.
	scratch, err := forge.NewSecret()
	require.NoError(t, err)
	d, err := f.Delivery(forge.Hook{Key: repo, Scheme: forge.SchemeHubSHA256, Secret: scratch}, "refs/heads/main", commit)
	require.NoError(t, err)
	mac := hmac.New(sha256.New, []byte(wire))
	mac.Write(d.Body)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, hook, bytes.NewReader(d.Body))
	require.NoError(t, err)
	req.Header = d.Header.Clone()
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

// installed is the hooks the fake forge holds on repo.
func installed(t *testing.T, f *fake.Forge, repo repokey.Key) []forge.InstalledPushHook {
	t.Helper()
	s, err := forges.For(repo, f.Token())
	require.NoError(t, err)
	hooks, err := s.ListPushHooks(t.Context(), repo)
	require.NoError(t, err)
	return hooks
}

// copyProject copies a project's documents, so a second directory carries
// the same project.yaml id.
func copyProject(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
}

func basic(user, token string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
}

func projectYAMLIDIn(t *testing.T, dir string) string {
	t.Helper()
	p, err := project.Open(dir)
	require.NoError(t, err)
	return p.ID()
}

func deployKeyPair(t *testing.T) (private, public string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(priv, "tau console")
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}
