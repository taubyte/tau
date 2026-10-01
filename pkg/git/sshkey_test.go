package git

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"gotest.tools/v3/assert"
)

// The account is a host fact, and an empty one would authenticate as nobody.
func TestSSHKeyAsNeedsAnAccount(t *testing.T) {
	_, host := hostKey(t)
	assert.ErrorContains(t, SSHKeyAs("", clientKey(t), []gossh.PublicKey{host})(&Repository{}), "account")
}

// No host keys is refused rather than read as "any".
func TestSSHKeyAsNeedsHostKeys(t *testing.T) {
	assert.ErrorContains(t, SSHKeyAs("git", clientKey(t), nil)(&Repository{}), "host keys")
}

// THE PIN IS WHAT THE CLONE ACTUALLY CHECKS. A real ssh server is dialled by a
// real clone: under the wrong pin the handshake is refused by name, and under
// the right one it gets past the host check and fails later, on the git
// protocol this server does not speak. A callback that was never wired in would
// let both through.
func TestACloneIsOnlyAnsweredByThePinnedHost(t *testing.T) {
	signer, host := hostKey(t)
	_, stranger := hostKey(t)
	addr := sshServer(t, signer)
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
	assert.Assert(t, err != nil, "this server speaks no git, so the clone cannot succeed")
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

// sshServer accepts any client key, completes the handshake, and serves no
// session -- enough to reach the host-key check and nothing past it.
func sshServer(t *testing.T, signer gossh.Signer) string {
	t.Helper()
	cfg := &gossh.ServerConfig{
		PublicKeyCallback: func(gossh.ConnMetadata, gossh.PublicKey) (*gossh.Permissions, error) { return nil, nil },
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc, chans, reqs, err := gossh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer sc.Close()
				go gossh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(gossh.Prohibited, "no git here")
				}
			}()
		}
	}()
	return ln.Addr().String()
}
