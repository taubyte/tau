package gittest_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
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

// A clone of what the server holds, with the one key it accepts, is a real
// checkout; any other key, or a repository it does not hold, is refused.
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

// With no allow, any key gets in.
func TestANilAllowAcceptsAnyKey(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	key, _ := gittest.KeyPair(t)
	s := gittest.NewServer(t, nil)
	st, _ := gittest.Repository(t, map[string]string{"a": "b"})
	s.Serve("/x/y.git", st)
	_, err := clone(t, s, "/x/y.git", key)
	require.NoError(t, err)
}
