package fake_test

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/drivers"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git/gittest"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// served is a GitHub-like forge on loopback and the registry a cloud declares for it:
// the real github driver, trusting the forge's own CA.
func served(t *testing.T) (*fake.Served, *forge.Registry) {
	t.Helper()
	s, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	reg, err := drivers.Registry([]drivers.Forge{{Host: s.Host(), Driver: drivers.GitHub, CA: s.CA()}})
	require.NoError(t, err)
	return s, reg
}

func TestTheGitHubDriverReadsAServedForge(t *testing.T) {
	s, reg := served(t)
	ctx := t.Context()
	keys := s.Keys()
	sess, err := reg.For(keys.Admin, s.Token())
	require.NoError(t, err)

	me, err := sess.Whoami(ctx)
	require.NoError(t, err)
	require.Equal(t, forge.Identity{Login: fake.Login, Subject: fake.Subject, Email: "fake-user@example.invalid"}, me,
		"the same person the in-memory driver names")

	for key, want := range map[repokey.Key]forge.Access{
		keys.Admin: forge.AccessAdmin, keys.Writable: forge.AccessWrite, keys.ReadOnly: forge.AccessRead,
	} {
		got, err := sess.Access(ctx, key)
		require.NoError(t, err, key)
		require.Equal(t, want, got, key)
	}
	for _, hidden := range []repokey.Key{keys.NoAccess, keys.Missing} {
		_, err := sess.Access(ctx, hidden)
		var missing *forge.NotFoundError
		require.ErrorAs(t, err, &missing, hidden)
	}

	repo, err := sess.Repository(ctx, keys.Admin)
	require.NoError(t, err)
	require.Equal(t, keys.Admin, repo.Key)
	require.Equal(t, "main", repo.DefaultBranch)

	var listed []string
	require.NoError(t, sess.ListRepositories(ctx, func(r forge.Repository) bool {
		listed = append(listed, r.Key.String())
		return true
	}))
	require.Contains(t, listed, keys.Admin.String())

	refused, err := reg.For(keys.Admin, s.BadToken())
	require.NoError(t, err)
	_, err = refused.Whoami(ctx)
	var credential *forge.CredentialError
	require.ErrorAs(t, err, &credential, "a token the forge refuses is a 401")
}

func TestTheGitHubDriverWritesToAServedForge(t *testing.T) {
	s, reg := served(t)
	ctx := t.Context()
	keys := s.Keys()
	sess, err := reg.For(keys.Admin, s.Token())
	require.NoError(t, err)

	made, err := sess.Create(ctx, keys.Creatable, forge.CreateOptions{Private: true})
	require.NoError(t, err)
	require.Equal(t, keys.Creatable, made.Key)
	_, err = sess.Create(ctx, keys.Uncreatable, forge.CreateOptions{})
	require.Error(t, err, "a namespace this credential may not write in")

	require.NoError(t, sess.Delete(ctx, keys.Deletable))
	_, err = sess.Repository(ctx, keys.Deletable)
	var missing *forge.NotFoundError
	require.ErrorAs(t, err, &missing, "gone once deleted")

	_, public := gittest.KeyPair(t)
	line := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(public)))
	handle, err := sess.InstallDeployKey(ctx, keys.Admin, "tau", line)
	require.NoError(t, err)
	installed, err := sess.ListDeployKeys(ctx, keys.Admin)
	require.NoError(t, err)
	require.Contains(t, installed, forge.InstalledDeployKey{Handle: handle, Title: "tau", PublicKey: line})
	require.NoError(t, sess.RemoveDeployKey(ctx, keys.Admin, handle))
	installed, err = sess.ListDeployKeys(ctx, keys.Admin)
	require.NoError(t, err)
	require.Empty(t, installed)

	secret, err := forge.NewSecret()
	require.NoError(t, err)
	callback, err := url.Parse("https://build.example.com/hook")
	require.NoError(t, err)
	hook, err := sess.RegisterPushHook(ctx, keys.Admin, callback, secret)
	require.NoError(t, err)
	hooks, err := sess.ListPushHooks(ctx, keys.Admin)
	require.NoError(t, err)
	require.Contains(t, hooks, forge.InstalledPushHook{Handle: hook.Handle, Callback: callback.String()})
	require.NoError(t, sess.RemovePushHook(ctx, keys.Admin, hook.Handle))
	hooks, err = sess.ListPushHooks(ctx, keys.Admin)
	require.NoError(t, err)
	require.Empty(t, hooks)
}

func TestAServedForgeIsTrustedOnlyByItsCA(t *testing.T) {
	s, _ := served(t)
	untrusting, err := drivers.Registry([]drivers.Forge{{Host: s.Host(), Driver: drivers.GitHub}})
	require.NoError(t, err)
	sess, err := untrusting.For(s.Keys().Admin, s.Token())
	require.NoError(t, err)
	_, err = sess.Whoami(t.Context())
	require.ErrorContains(t, err, "certificate", "nothing the machine trusts signed it")
}

func TestOnePortAnswersGitOverTLSAndInTheClear(t *testing.T) {
	s, _ := served(t)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM([]byte(s.CA())))
	refs := "/" + s.Keys().Admin.FullName() + ".git/info/refs?service=git-upload-pack"

	safe := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}
	resp, err := safe.Get("https://" + s.Host() + refs)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "git over TLS asks for its credential")

	resp, err = http.Get("http://" + s.Host() + refs)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "and in the clear the same")

	resp, err = safe.Get("https://" + s.Host() + "/api/v3/user")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the API wants a token")

	// A connection that says nothing and leaves is let go.
	conn, err := net.Dial("tcp", s.Host())
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	gone, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	require.NoError(t, gone.Close())
	_, err = http.Get("http://" + gone.Host() + refs)
	require.Error(t, err, "a closed forge answers nothing")
}

func TestAShapeThatSpeaksNoAPIServesOnlyGit(t *testing.T) {
	s, err := fake.Serve(fake.GitLabLike)
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	resp, err := http.Get("http://" + s.Host() + "/api/v3/user")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
