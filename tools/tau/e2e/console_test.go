//go:build dreaming

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/pkg/linkcreds"
	"github.com/taubyte/tau/tools/tau/internal/config"
)

// A console holds a tau token and the forge credential its sign-in handed
// back, and calls the forge itself. This drives what the console note says a
// client writes, with nothing but a forge session and plain HTTP to the cloud:
// a repository created and pushed at the forge, its hook and deploy key
// installed there, the project registered, the credentials handed over, and a
// push confirmed and queued. The cloud is asked nothing about any forge.
func TestTheConsolePathAgainstARealFleet(t *testing.T) {
	t.Setenv(config.EnvConfig, filepath.Join(t.TempDir(), "config.yaml"))
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvCloud, "")
	gitEnv(t)

	f, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	cloud, _ := fleet(t, f)

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
	forgeToken := cfg.Profiles[cfg.Current].Forges[f.Host()].Token
	require.NotEmpty(t, forgeToken)

	call := func(method, route string, body io.Reader) (int, []byte) {
		t.Helper()
		if body == nil {
			body = http.NoBody
		}
		req, err := http.NewRequestWithContext(t.Context(), method, cloud+route, body)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, out
	}

	code, out := call(http.MethodGet, "/cloud", nil)
	require.Equal(t, http.StatusOK, code, string(out))
	var named struct{ Fqdn string }
	require.NoError(t, json.Unmarshal(out, &named))
	require.Equal(t, e2eCloud, named.Fqdn)

	// At the forge, as the user: a repository in an organization, pushed.
	repo := repokey.Key{Host: f.Host(), Owner: fake.Namespace, Name: "console-site"}
	s, err := forges.For(repo, forge.Token(forgeToken))
	require.NoError(t, err)
	_, err = s.Create(t.Context(), repo, forge.CreateOptions{Private: true})
	require.NoError(t, err)
	work := t.TempDir()
	ok(t, "init", "site", "--dir", work)
	project := projectYAMLIDIn(t, work)
	addr, err := s.CloneAddress(repo, forge.HTTPS)
	require.NoError(t, err)
	auth := "Authorization: Basic " + basic(addr.HTTPUsername, forgeToken)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "site"}, {"-c", "http.extraHeader=" + auth, "push", "-q", addr.URL, "HEAD:refs/heads/main"}} {
		gitOK(t, work, args...)
	}
	commit := strings.TrimSpace(gitOut(t, work, "rev-parse", "HEAD"))

	secret, err := forge.NewSecret()
	require.NoError(t, err)
	callback, err := url.Parse("https://build-frontend." + named.Fqdn + "/hook/" + project)
	require.NoError(t, err)
	hook, err := s.RegisterPushHook(t.Context(), repo, callback, secret)
	require.NoError(t, err)
	private, public := deployKeyPair(t)
	_, err = s.InstallDeployKey(t.Context(), repo, "tau console", public)
	require.NoError(t, err)

	// At the cloud: register, then hand over the two credentials.
	body, err := json.Marshal(map[string]any{"config_repository_key": repo.String(), "code_repository_keys": []string{}})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		code, out = call(http.MethodPost, "/projects/"+project, bytes.NewReader(body))
		return code == http.StatusCreated
	}, 2*time.Minute, time.Second, "registering: %d %s", code, out)
	for name, value := range map[string][]byte{linkcreds.HookSecret(repo.String()): secret.Expose(), linkcreds.DeployKey(repo.String()): []byte(private)} {
		code, out = call(http.MethodPut, "/secrets/"+project+"/"+linkcreds.Environment+"/"+name, bytes.NewReader(value))
		require.Equal(t, http.StatusNoContent, code, string(out))
	}
	code, out = call(http.MethodGet, "/projects", nil)
	require.Equal(t, http.StatusOK, code, string(out))
	require.Contains(t, string(out), project)

	wire, err := hook.Scheme.Wire(secret)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return deliver(t, f.Forge, cloud+"/hook/"+project, repo, wire, commit).StatusCode == http.StatusAccepted
	}, time.Minute, time.Second, "the push was never confirmed")
	require.Eventually(t, func() bool {
		code, out = call(http.MethodGet, "/jobs/"+project, nil)
		var ids []string
		return code == http.StatusOK && json.Unmarshal(out, &ids) == nil && len(ids) == 1
	}, time.Minute, time.Second, "the push queued no job")

	// A rotation, as the client makes it: a new secret installed at the forge,
	// then handed to the cloud. A delivery signed with the old one — in flight,
	// or from a hook not yet updated — still verifies for the grace; the new
	// one verifies at once; anything else is refused.
	rotated, err := forge.NewSecret()
	require.NoError(t, err)
	require.NoError(t, s.RemovePushHook(t.Context(), repo, hook.Handle))
	newHook, err := s.RegisterPushHook(t.Context(), repo, callback, rotated)
	require.NoError(t, err)
	code, out = call(http.MethodPut, "/secrets/"+project+"/"+linkcreds.Environment+"/"+linkcreds.HookSecret(repo.String()), bytes.NewReader(rotated.Expose()))
	require.Equal(t, http.StatusNoContent, code, string(out))

	newWire, err := newHook.Scheme.Wire(rotated)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return deliver(t, f.Forge, cloud+"/hook/"+project, repo, newWire, commit).StatusCode == http.StatusAccepted
	}, time.Minute, time.Second, "the rotated secret never verified")
	require.Equal(t, http.StatusAccepted, deliver(t, f.Forge, cloud+"/hook/"+project, repo, wire, commit).StatusCode,
		"the replaced secret is refused inside its grace")

	stranger, err := forge.NewSecret()
	require.NoError(t, err)
	strangerWire, err := newHook.Scheme.Wire(stranger)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, deliver(t, f.Forge, cloud+"/hook/"+project, repo, strangerWire, commit).StatusCode)
}

func gitOK(t *testing.T, dir string, args ...string) { gitOut(t, dir, args...) }

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "credential.helper="}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}
