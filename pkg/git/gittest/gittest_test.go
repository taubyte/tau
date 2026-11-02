package gittest_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/pkg/git/gittest"
)

func clone(t *testing.T, s *gittest.Server, path, key string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	root := t.TempDir()
	r, err := git.New(ctx, git.URL(s.URL(path)), git.Root(root), git.Branch("main"), git.Output(io.Discard),
		git.SSHKeyAs("git", key, []gossh.PublicKey{s.HostKey}))
	if err != nil {
		return "", err
	}
	return r.Dir(), nil
}

func TestAServerServesOnlyTheKeyItAccepts(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	good, pub := gittest.KeyPair(t)
	bad, _ := gittest.KeyPair(t)
	s := gittest.NewServer(t, func(k gossh.PublicKey) bool { return string(k.Marshal()) == string(pub.Marshal()) })
	st, commit := gittest.Repository(t, map[string]string{"README": "hello"})
	require.Len(t, commit, 40)
	s.Serve("/acme/pay.git", st)

	dir, err := clone(t, s, "/acme/pay.git", good)
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(dir, "README"))
	require.NoError(t, err)
	require.Equal(t, "hello", string(body))
	require.Equal(t, 1, s.Served())

	_, err = clone(t, s, "/acme/pay.git", bad)
	require.Error(t, err)
	_, err = clone(t, s, "/acme/nope.git", good)
	require.Error(t, err)
	require.Equal(t, 1, s.Served(), "neither refusal read a pack")
}

func TestANilAllowAcceptsAnyKey(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	key, _ := gittest.KeyPair(t)
	s := gittest.NewServer(t, nil)
	st, _ := gittest.Repository(t, map[string]string{"a": "b"})
	s.Serve("/x/y.git", st)
	_, err := clone(t, s, "/x/y.git", key)
	require.NoError(t, err)
}

// dial is a raw ssh client on the server, for asking it what no git client
// would.
func dial(t *testing.T, s *gittest.Server) *gossh.Client {
	t.Helper()
	key, _ := gittest.KeyPair(t)
	signer, err := gossh.ParsePrivateKey([]byte(key))
	require.NoError(t, err)
	c, err := gossh.Dial("tcp", s.Addr, &gossh.ClientConfig{
		User:            "git",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.FixedHostKey(s.HostKey),
		Timeout:         10 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestTheServerAnswersOnlyUploadPack(t *testing.T) {
	s := gittest.NewServer(t, nil)
	st, _ := gittest.Repository(t, map[string]string{"a": "b"})
	s.Serve("/x/y.git", st)
	c := dial(t, s)

	_, _, err := c.OpenChannel("direct-tcpip", nil)
	require.ErrorContains(t, err, "session only")

	ask := func(kind string, payload []byte) bool {
		ch, reqs, err := c.OpenChannel("session", nil)
		require.NoError(t, err)
		defer ch.Close()
		go gossh.DiscardRequests(reqs)
		ok, err := ch.SendRequest(kind, true, payload)
		require.NoError(t, err)
		return ok
	}
	require.False(t, ask("shell", nil), "a shell was granted")
	require.False(t, ask("exec", []byte{0xff}), "a malformed exec was granted")
	type testCase struct{ Command string }
	require.False(t, ask("exec", gossh.Marshal(testCase{"git-receive-pack '/x/y.git'"})), "a push was granted")
	require.Equal(t, 0, s.Served())
}

func TestAnUnknownRepositoryExitsNonZero(t *testing.T) {
	s := gittest.NewServer(t, nil)
	sess, err := dial(t, s).NewSession()
	require.NoError(t, err)
	defer sess.Close()
	var stderr strings.Builder
	sess.Stderr = &stderr
	err = sess.Run("git-upload-pack '/no/such.git'")
	var exit *gossh.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 1, exit.ExitStatus())
	require.Contains(t, stderr.String(), "repository not found")
	require.Equal(t, 0, s.Served())
}
