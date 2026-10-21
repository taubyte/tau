package fake_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
)

func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// A served forge is a git remote: what the token may write it pushes, what
// it may read it clones, and the grants are the ones every face answers with.
func TestAServedForgeServesGit(t *testing.T) {
	s, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	defer s.Close()

	reg, err := s.Registry(forge.Policy{})
	require.NoError(t, err)
	sess, err := reg.For(s.Keys().Writable, s.Token())
	require.NoError(t, err)
	addr, err := sess.CloneAddress(s.Keys().Writable, forge.HTTPS)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addr.URL, "http://"+s.Host()+"/"), addr.URL)
	with := func(url, token string) string {
		return strings.Replace(url, "http://", "http://"+addr.HTTPUsername+":"+token+"@", 1)
	}

	work := t.TempDir()
	_, err = runGit(t, work, "init", "--initial-branch=main")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(work, "README"), []byte("hi"), 0o644))
	_, err = runGit(t, work, "add", "README")
	require.NoError(t, err)
	_, err = runGit(t, work, "commit", "-m", "first")
	require.NoError(t, err)

	out, err := runGit(t, work, "push", with(addr.URL, s.TokenString()), "main")
	require.NoError(t, err, out)

	clone := t.TempDir()
	out, err = runGit(t, clone, "clone", with(addr.URL, s.TokenString()), "repo")
	require.NoError(t, err, out)
	got, err := os.ReadFile(filepath.Join(clone, "repo", "README"))
	require.NoError(t, err)
	require.Equal(t, "hi", string(got))

	// Read-only pushes nothing; a revoked token and a hidden repository clone
	// nothing; a public one reads to anyone.
	ro, err := reg.For(s.Keys().ReadOnly, s.Token())
	require.NoError(t, err)
	roAddr, err := ro.CloneAddress(s.Keys().ReadOnly, forge.HTTPS)
	require.NoError(t, err)
	out, err = runGit(t, work, "push", with(roAddr.URL, s.TokenString()), "main")
	require.Error(t, err, out)
	out, err = runGit(t, t.TempDir(), "clone", with(roAddr.URL, s.BadTokenString()), "repo")
	require.NoError(t, err, "a public repository reads to anyone: %s", out)
	out, err = runGit(t, t.TempDir(), "clone", with(addr.URL, s.BadTokenString()), "repo")
	require.Error(t, err, out)
	out, err = runGit(t, t.TempDir(), "clone", addr.URL, "repo")
	require.Error(t, err, out)
	out, err = runGit(t, t.TempDir(), "clone", with("http://"+s.Host()+"/acme/nope.git", s.TokenString()), "repo")
	require.Error(t, err, out)
}
