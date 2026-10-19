//go:build dreaming

// Package e2e runs the CLI against a real v2 fleet: a dream universe of two
// nodes, one running the build frontend (the public role) and the other every
// private service it reaches — auth, the build backend, secrets, register.
// `tau link` installs on the same fake forge the fleet verifies with, so a
// delivery signed with what the link installed is the real check. The
// forge is pkg/forge/fake, so "write access" is a real answer from a real
// driver, just not one that needs the internet.
package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/linkcreds"
	"github.com/taubyte/tau/tools/tau/internal/cli"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

type result struct {
	code           int
	stdout, stderr string
}

// forges is what `tau link` installs on: the same fake forge the fleet
// verifies against.
var forges *forge.Registry

func tau(args ...string) result {
	var out, errs bytes.Buffer
	code := cli.Run(cli.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errs, HTTP: http.DefaultClient, Forges: forges}, args)
	return result{code, out.String(), errs.String()}
}

func ok(t *testing.T, args ...string) string {
	t.Helper()
	r := tau(args...)
	require.Equal(t, 0, r.code, "tau %s\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.stdout, r.stderr)
	return r.stdout
}

func TestTheCLIAgainstARealFleet(t *testing.T) {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")

	f := fake.New(fake.GitHubLike)
	// A development fleet answers over http, which a forge refuses to deliver
	// to unless told; the fleet's own copy verifies with the same drivers.
	var err error
	forges, err = f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	t.Cleanup(func() { forges = nil })

	u, err := dream.Start(t.Context(), t.Name(), dream.Config{
		Copies:  2,
		Options: []node.Option{node.WithForges(forges)},
		Configure: func(index int, c *node.Config) {
			if index == 1 {
				c.Services = []string{node.Discovery, node.BuildFrontend}
				c.BuildFrontend.Listen = "127.0.0.1:0"
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)

	front, found := dream.Node(u.Pids()[1])
	require.True(t, found)
	addr := front.BuildFrontendAddr()
	require.NotNil(t, addr)
	cloud := "http://" + addr.String()

	ok(t, "login", "--cloud", cloud, "--token", f.TokenString())

	// A generated project compiles and audits clean, with no cloud involved.
	dir := t.TempDir()
	ok(t, "init", "shop", "--dir", dir)
	require.Contains(t, ok(t, "validate", "--dir", dir), "✓ compiles")

	// Linking claims project.yaml's own id in the register, through the
	// frontend's one public door to it, and installs the webhook and a deploy
	// key on the fake forge. The fleet has just booted, so the frontend may not
	// have found a register or a vault yet.
	configRepo, codeRepo := f.Keys().Admin, f.Keys().Deletable
	var link result
	require.Eventually(t, func() bool {
		link = tau("link", "--dir", dir, "--config-repo", configRepo.String(), "--code-repo", codeRepo.String(), "-o", "json")
		return link.code == 0
	}, 2*time.Minute, time.Second, "link never succeeded: %s", link.stderr)
	var linked config.Link
	require.NoError(t, json.Unmarshal([]byte(link.stdout), &linked))
	require.NotEmpty(t, linked.ProjectID)
	require.Equal(t, []string{codeRepo.String()}, linked.Code)

	// The forge holds the webhook, aimed at this project's own hook route, and
	// a read-only deploy key.
	wire := f.LastHookWire()
	require.NotEmpty(t, wire)
	require.True(t, f.LastDeployKeyReadOnly())

	// The link's credentials are on the cloud, by name only.
	var names []string
	require.NoError(t, json.Unmarshal([]byte(ok(t, "secrets", "list", "--dir", dir, "--env", linkcreds.Environment, "-o", "json")), &names))
	require.ElementsMatch(t, []string{linkcreds.HookSecret, linkcreds.DeployKey(configRepo.String()), linkcreds.DeployKey(codeRepo.String())}, names)

	// The same id again, from a repository that did not claim it, is refused
	// by name.
	other := t.TempDir()
	require.NoError(t, copyProject(dir, other))
	r := tau("link", "--dir", other, "--config-repo", f.Keys().Writable.String(), "--manual")
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "already claimed by "+configRepo.String()+"; generate a new id in project.yaml")

	// A token that may not write to the repositories is refused, by the forge.
	r = tau("link", "--dir", t.TempDir(), "--config-repo", f.Keys().NoAccess.String())
	require.NotZero(t, r.code)

	// THE FIRST PUSH VERIFIES BEFORE ANYTHING IS PUBLISHED: the forge signs a
	// delivery with the wire secret the link installed, and the cloud checks it
	// against the secret the link stored. A wrong signature is refused.
	hook := cloud + "/hook/" + linked.ProjectID
	require.Equal(t, http.StatusAccepted, deliver(t, f, hook, configRepo, wire).StatusCode)
	require.Equal(t, http.StatusForbidden, deliver(t, f, hook, configRepo, "not-the-secret").StatusCode)
	require.Equal(t, http.StatusNotFound, deliver(t, f, cloud+"/hook/QmNobodyLinkedThisProjectId", configRepo, wire).StatusCode)
	var jobs []any
	require.Eventually(t, func() bool {
		jobs = nil
		return json.Unmarshal([]byte(ok(t, "jobs", "--dir", dir, "-o", "json")), &jobs) == nil && len(jobs) == 1
	}, time.Minute, time.Second, "the verified push queued a job")

	// Secrets: set, then listed by name only.
	require.Eventually(t, func() bool {
		return tau("secrets", "set", "DB_URL", "postgres://x", "--dir", dir, "--env", "prod").code == 0
	}, time.Minute, time.Second)
	names = nil
	require.NoError(t, json.Unmarshal([]byte(ok(t, "secrets", "list", "--dir", dir, "--env", "prod", "-o", "json")), &names))
	require.Equal(t, []string{"DB_URL"}, names)
	ok(t, "secrets", "unset", "DB_URL", "--dir", dir, "--env", "prod", "--yes")

	require.Contains(t, ok(t, "status", "--dir", dir), linked.ProjectID)
	require.Contains(t, ok(t, "projects", "--forge", f.Host()), linked.ProjectID)

	// A token the forge does not know is refused as unauthenticated.
	t.Setenv(config.EnvToken, f.BadTokenString())
	r = tau("jobs", "--dir", dir)
	require.NotZero(t, r.code)
	require.Contains(t, r.stderr, "unauthorized")
}

// deliver posts a push for repo to hook, signed the way a hub-sha256 forge
// signs: an HMAC of the body keyed with the wire secret it was given.
func deliver(t *testing.T, f *fake.Forge, hook string, repo repokey.Key, wire string) *http.Response {
	t.Helper()
	// The fake builds the payload; it is re-signed with the installed wire.
	scratch, err := forge.NewSecret()
	require.NoError(t, err)
	d, err := f.Delivery(forge.Hook{Key: repo, Scheme: forge.SchemeHubSHA256, Secret: scratch}, "refs/heads/main", strings.Repeat("a", 40))
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
