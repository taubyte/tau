package git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"

	"github.com/taubyte/tau/pkg/git/gittest"
)

func TestSSHKeyAsNeedsAnAccount(t *testing.T) {
	_, host := hostKey(t)
	assert.ErrorContains(t, SSHKeyAs("", clientKey(t), []gossh.PublicKey{host})(&Repository{}), "account")
}

func TestSSHKeyAsNeedsHostKeys(t *testing.T) {
	assert.ErrorContains(t, SSHKeyAs("git", clientKey(t), nil)(&Repository{}), "host keys")
}

func TestACloneIsOnlyAnsweredByThePinnedHost(t *testing.T) {
	server := gittest.NewServer(t, nil)
	host := server.HostKey
	_, stranger := hostKey(t)
	addr := server.Addr
	key := clientKey(t)

	clone := func(pinned gossh.PublicKey) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := New(ctx,
			URL("ssh://git@"+addr+"/acme/pay.git"),
			Root(t.TempDir()),
			Output(io.Discard),
			SSHKeyAs("git", key, []gossh.PublicKey{pinned}))
		return err
	}

	err := clone(stranger)
	assert.ErrorContains(t, err, "not one pinned")

	err = clone(host)
	assert.Assert(t, err != nil, "this server holds no repository, so the clone cannot succeed")
	assert.Assert(t, !strings.Contains(err.Error(), "not one pinned"), "the pinned host was refused: %v", err)
}

func hostKey(t *testing.T) (gossh.Signer, gossh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	assert.NilError(t, err)
	signer, err := gossh.NewSignerFromKey(priv)
	assert.NilError(t, err)
	return signer, signer.PublicKey()
}

func clientKey(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	assert.NilError(t, err)
	block, err := gossh.MarshalPrivateKey(priv, "")
	assert.NilError(t, err)
	return string(pem.EncodeToMemory(block))
}
