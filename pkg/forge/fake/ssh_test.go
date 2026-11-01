package fake_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/forge/fake"
	"github.com/taubyte/tau/pkg/git"
)

func deployKeyPair(t *testing.T) (string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := gossh.MarshalPrivateKey(priv, "")
	require.NoError(t, err)
	sp, err := gossh.NewPublicKey(pub)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(gossh.MarshalAuthorizedKey(sp)))
}

func TestAServedForgeServesGitOverSSHToDeployKeys(t *testing.T) {
	ctx := context.Background()
	s, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	defer s.Close()

	reg, err := s.Registry(forge.Policy{})
	require.NoError(t, err)
	k := s.Keys().Admin
	sess, err := reg.For(k, s.Token())
	require.NoError(t, err)
	https, err := sess.CloneAddress(k, forge.HTTPS)
	require.NoError(t, err)
	addr, err := sess.CloneAddress(k, forge.SSH)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(addr.URL, "ssh://git@127.0.0.1:"), addr.URL)
	require.NotContains(t, addr.URL, s.Host())
	viaSSH, err := reg.Key(addr.URL)
	require.NoError(t, err)
	viaHTTPS, err := reg.Key(https.URL)
	require.NoError(t, err)
	require.Equal(t, viaHTTPS, viaSSH)
	require.Equal(t, k, viaSSH)
	repo, err := sess.Repository(ctx, k)
	require.NoError(t, err)
	require.Equal(t, addr.URL, repo.SSH)

	work := t.TempDir()
	for _, args := range [][]string{{"init", "--initial-branch=main"}, {"add", "README"}, {"commit", "-m", "first"}} {
		if args[0] == "add" {
			require.NoError(t, os.WriteFile(filepath.Join(work, "README"), []byte("hi"), 0o644))
		}
		out, err := runGit(t, work, args...)
		require.NoError(t, err, out)
	}
	pushURL := strings.Replace(https.URL, "http://", "http://"+https.HTTPUsername+":"+s.TokenString()+"@", 1)
	out, err := runGit(t, work, "push", pushURL, "main")
	require.NoError(t, err, out)
	head, err := runGit(t, work, "rev-parse", "HEAD")
	require.NoError(t, err)

	hostKey, _, _, _, err := gossh.ParseAuthorizedKey([]byte(s.HostKey()))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(s.HostKey(), "ssh-ed25519 AAAA"))
	pinned := []gossh.PublicKey{hostKey}
	priv, pub := deployKeyPair(t)
	lsRemote := func(key string, pins []gossh.PublicKey) error {
		_, err := git.ListRemote(ctx, git.URL(addr.URL), git.SSHKeyAs("git", key, pins))
		return err
	}

	require.Error(t, lsRemote(priv, pinned), "a key installed nowhere reads nothing")

	h, err := sess.InstallDeployKey(ctx, k, "build", pub)
	require.NoError(t, err)

	refs, err := git.ListRemote(ctx, git.URL(addr.URL), git.SSHKeyAs("git", priv, pinned))
	require.NoError(t, err)
	var seen bool
	for _, r := range refs {
		if r.Name().String() == "refs/heads/main" {
			seen = r.Hash().String() == head
		}
	}
	require.True(t, seen, "ls-remote sees the pushed commit: %v", refs)

	root := filepath.Join(t.TempDir(), "clone")
	_, err = git.New(ctx, git.URL(addr.URL), git.Root(root), git.Output(io.Discard), git.SSHKeyAs("git", priv, pinned))
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(root, "README"))
	require.NoError(t, err)
	require.Equal(t, "hi", string(got))

	other, _ := deployKeyPair(t)
	require.Error(t, lsRemote(other, pinned), "a key not installed is refused")
	_, wrongHost := deployKeyPair(t)
	wrong, _, _, _, err := gossh.ParseAuthorizedKey([]byte(wrongHost))
	require.NoError(t, err)
	require.Error(t, lsRemote(priv, []gossh.PublicKey{wrong}), "a host key that is not pinned is refused")

	// A key installed on another repository reads nothing here.
	oh, err := sess.InstallDeployKey(ctx, s.Keys().Deletable, "other", pub)
	require.NoError(t, err)
	otherAddr, err := sess.CloneAddress(s.Keys().Writable, forge.SSH)
	require.NoError(t, err)
	_, err = git.ListRemote(ctx, git.URL(otherAddr.URL), git.SSHKeyAs("git", priv, pinned))
	require.Error(t, err)
	require.NoError(t, sess.RemoveDeployKey(ctx, s.Keys().Deletable, oh))

	// Deploy keys are read-only: a push over ssh is refused.
	cloned, err := git.New(ctx, git.URL(addr.URL), git.Root(root), git.Output(io.Discard), git.SSHKeyAs("git", priv, pinned))
	require.NoError(t, err)
	require.Error(t, cloned.Repo().PushContext(ctx, pushOptions(t, priv, pinned)))

	require.NoError(t, sess.RemoveDeployKey(ctx, k, h))
	require.Error(t, lsRemote(priv, pinned), "a removed deploy key is revoked")
}

func TestANestingForgeCreatesUnderASubgroup(t *testing.T) {
	f := fake.New(fake.GitLabLike)
	reg, err := f.Registry(forge.Policy{})
	require.NoError(t, err)
	k, err := reg.Key("https://" + f.Host() + "/" + fake.Namespace + "/team/fresh.git")
	require.NoError(t, err)
	sess, err := reg.For(k, f.Token())
	require.NoError(t, err)
	_, err = sess.Create(context.Background(), k, forge.CreateOptions{Private: true})
	require.NoError(t, err)
}

func pushOptions(t *testing.T, priv string, pinned []gossh.PublicKey) *gogit.PushOptions {
	t.Helper()
	auth, err := gitssh.NewPublicKeys("git", []byte(priv), "")
	require.NoError(t, err)
	auth.HostKeyCallback = gossh.FixedHostKey(pinned[0])
	return &gogit.PushOptions{Auth: auth, RefSpecs: []gitconfig.RefSpec{"refs/heads/main:refs/heads/other"}}
}

func TestTheSSHEndpointIsASpellingOfTheForge(t *testing.T) {
	s, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	defer s.Close()
	sp := s.Driver().(forge.Spellings)
	require.Len(t, sp.SpellingHosts(), 1)
	k := s.Keys().Writable
	elsewhere := k
	elsewhere.Host = "elsewhere.example"
	_, ok := sp.Canonical(elsewhere)
	require.False(t, ok)
	got, ok := sp.Canonical(k)
	require.True(t, ok)
	require.Equal(t, k, got)

	plain := fake.New(fake.GitHubLike).Driver().(forge.Spellings)
	require.Empty(t, plain.SpellingHosts())
	_, ok = plain.Canonical(elsewhere)
	require.False(t, ok)
}

func TestTheSSHEndpointRunsUploadPackOnly(t *testing.T) {
	ctx := context.Background()
	s, err := fake.Serve(fake.GitHubLike)
	require.NoError(t, err)
	defer s.Close()
	reg, err := s.Registry(forge.Policy{})
	require.NoError(t, err)
	k := s.Keys().Admin
	sess, err := reg.For(k, s.Token())
	require.NoError(t, err)
	priv, pub := deployKeyPair(t)
	_, err = sess.InstallDeployKey(ctx, k, "build", pub)
	require.NoError(t, err)
	addr, err := sess.CloneAddress(k, forge.SSH)
	require.NoError(t, err)
	hostKey, _, _, _, err := gossh.ParseAuthorizedKey([]byte(s.HostKey()))
	require.NoError(t, err)

	_, err = git.ListRemote(ctx, git.URL(addr.URL), git.SSHKeyAs("git", priv, []gossh.PublicKey{hostKey}))
	require.ErrorIs(t, err, transport.ErrEmptyRemoteRepository)

	signer, err := gossh.ParsePrivateKey([]byte(priv))
	require.NoError(t, err)
	host := strings.TrimPrefix(addr.URL, "ssh://git@")
	host = host[:strings.Index(host, "/")]
	client, err := gossh.Dial("tcp", host, &gossh.ClientConfig{
		User: "git", Auth: []gossh.AuthMethod{gossh.PublicKeys(signer)}, HostKeyCallback: gossh.FixedHostKey(hostKey),
	})
	require.NoError(t, err)
	defer client.Close()

	_, _, err = client.OpenChannel("direct-tcpip", nil)
	require.Error(t, err)

	for _, cmd := range []string{"ls", "git-archive '/acme/site.git'", "git-upload-pack '/%zz'", "git-upload-pack '/acme/nope.git'", "git-receive-pack '/acme/site.git'"} {
		ss, err := client.NewSession()
		require.NoError(t, err)
		require.Error(t, ss.Run(cmd), cmd)
	}
	ss, err := client.NewSession()
	require.NoError(t, err)
	require.Error(t, ss.Shell())
	ss.Close()
}
