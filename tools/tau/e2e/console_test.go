//go:build dreaming

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node"
	"github.com/taubyte/tau/node/dream"
	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// A console holds one tau token and no forge credential, and does all of its
// forge work through the build frontend. This drives that whole path against
// a fleet and a fake forge that really serves git: a repository created, a
// project's files committed into it and read back, a clone and a push through
// /git/, the project linked, and a push to it queuing a build.
func TestTheConsolePathAgainstARealFleet(t *testing.T) {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")

	f, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	forges, err = f.Registry(forge.Policy{AllowInsecureCallback: true})
	require.NoError(t, err)
	t.Cleanup(func() { forges = nil })

	u, err := dream.Start(t.Context(), t.Name(), dream.Config{
		Copies:  2,
		Options: []node.Option{node.WithForges(forges)},
		Configure: func(index int, c *node.Config) {
			c.Gateway.CloudFqdn = e2eCloud
			if index > 0 {
				c.Services = []string{node.Discovery, node.BuildFrontend}
				c.BuildFrontend.Listen = "127.0.0.1:0"
			}
		},
	})
	require.NoError(t, err)
	t.Cleanup(u.Close)
	front, found := dream.Node(u.Pids()[1])
	require.True(t, found)
	cloud := "http://" + front.BuildFrontendAddr().String()

	var login result
	require.Eventually(t, func() bool {
		login = tauIn(f.TokenString()+"\n", "login", "--cloud", cloud, "--sso", f.Host(), "--paste")
		return login.code == 0
	}, 2*time.Minute, time.Second, "login never succeeded: %s", login.stderr)
	path, err := config.Path()
	require.NoError(t, err)
	cfg, err := config.Load(path)
	require.NoError(t, err)
	bearer := cfg.Profiles[cfg.Current].Token
	require.NotEmpty(t, bearer)

	call := func(method, route string, body any) (int, []byte) {
		t.Helper()
		var in io.Reader = http.NoBody
		if body != nil {
			b, err := json.Marshal(body)
			require.NoError(t, err)
			in = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, cloud+route, in)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, out
	}

	host := f.Host()
	configRepo := repokey.Key{Host: host, Owner: "acme", Name: "shop-config"}
	codeRepo := repokey.Key{Host: host, Owner: "acme", Name: "shop-code"}

	// Two repositories made, as the caller; the same name again is taken.
	for _, k := range []repokey.Key{configRepo, codeRepo} {
		var code int
		var out []byte
		require.Eventually(t, func() bool {
			code, out = call(http.MethodPost, "/forges/"+host+"/repositories", map[string]any{"repository": k.String(), "private": true})
			return code == http.StatusCreated
		}, 2*time.Minute, time.Second, "creating %s: %d %s", k, code, out)
	}
	code, out := call(http.MethodPost, "/forges/"+host+"/repositories", map[string]any{"repository": configRepo.String()})
	require.Equal(t, http.StatusConflict, code, string(out))
	code, out = call(http.MethodGet, "/forges/"+host+"/repositories", nil)
	require.Equal(t, http.StatusOK, code, string(out))
	require.Contains(t, string(out), f.Keys().Writable.String(), "the forge's own listing")

	// The project's files, committed into the empty config repository in one
	// commit, and read back.
	dir := t.TempDir()
	ok(t, "init", "shop", "--dir", dir)
	var files []map[string]any
	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, map[string]any{"path": filepath.ToSlash(rel), "content": data})
		return nil
	}))
	require.NotEmpty(t, files)
	code, out = call(http.MethodPost, "/forges/"+host+"/commit", map[string]any{"repository": configRepo.String(), "message": "a new project", "files": files})
	require.Equal(t, http.StatusCreated, code, string(out))
	var committed struct{ Branch, Commit string }
	require.NoError(t, json.Unmarshal(out, &committed))
	require.Equal(t, "main", committed.Branch)

	first := files[0]["path"].(string)
	code, out = call(http.MethodPost, "/forges/"+host+"/read", map[string]any{"repository": configRepo.String(), "path": first})
	require.Equal(t, http.StatusOK, code, string(out))
	var read struct {
		Commit  string
		Content []byte
	}
	require.NoError(t, json.Unmarshal(out, &read))
	require.Equal(t, committed.Commit, read.Commit)
	require.Equal(t, files[0]["content"], read.Content)

	// A clone and a push through /git/, carrying the bearer and nothing else.
	remote := cloud + "/git/" + configRepo.String() + ".git"
	work := t.TempDir()
	gitOK(t, work, bearer, "clone", remote, "repo")
	repo := filepath.Join(work, "repo")
	require.FileExists(t, filepath.Join(repo, first))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "NOTES"), []byte("edited in a browser"), 0o644))
	gitOK(t, repo, bearer, "add", "NOTES")
	gitOK(t, repo, bearer, "commit", "-m", "an edit")
	gitOK(t, repo, bearer, "push", "origin", "main")
	code, out = call(http.MethodPost, "/forges/"+host+"/read", map[string]any{"repository": configRepo.String(), "path": "NOTES"})
	require.Equal(t, http.StatusOK, code, string(out))
	require.NoError(t, json.Unmarshal(out, &read))
	require.Equal(t, "edited in a browser", string(read.Content))

	// Linked, and the project read back by id; then a push to it queues a
	// build.
	var link result
	require.Eventually(t, func() bool {
		link = tau("link", "--dir", dir, "--config-repo", configRepo.String(), "--code-repo", codeRepo.String(), "-o", "json")
		return link.code == 0
	}, 2*time.Minute, time.Second, "link never succeeded: %s", link.stderr)
	var linked config.Link
	require.NoError(t, json.Unmarshal([]byte(link.stdout), &linked))
	code, out = call(http.MethodGet, "/projects/"+linked.ProjectID, nil)
	require.Equal(t, http.StatusOK, code, string(out))
	require.Contains(t, string(out), configRepo.String())

	wire := f.LastHookWire()
	require.Equal(t, http.StatusAccepted, deliver(t, f.Forge, cloud+"/hook/"+linked.ProjectID, configRepo, wire).StatusCode)
	require.Eventually(t, func() bool {
		var jobs []any
		return json.Unmarshal([]byte(ok(t, "jobs", "--dir", dir, "-o", "json")), &jobs) == nil && len(jobs) == 1
	}, time.Minute, time.Second, "the push queued no job")
}

func gitOK(t *testing.T, dir, bearer string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "http.extraHeader=Authorization: Bearer " + bearer, "-c", "credential.helper="}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}
