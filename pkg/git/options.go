package git

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	gossh "golang.org/x/crypto/ssh"
)

type Option func(c *Repository) error

// URL sets the repository URL.
func URL(url string) Option {
	return func(c *Repository) error {
		c.url = url
		return nil
	}
}

// Output sets where progress is written.
func Output(output io.Writer) Option {
	return func(c *Repository) error {
		c.output = output
		return nil
	}
}

// SSHKeyAs authenticates as account with key, and only to a host presenting one of
// hostKeys. The account is a host fact, named by the forge driver. Host keys are
// required: an ssh connection accepting any host key would let whoever sits on the
// path hand a build different code.
func SSHKeyAs(account, key string, hostKeys []gossh.PublicKey) Option {
	if len(hostKeys) == 0 {
		return func(*Repository) error {
			return errors.New("an ssh clone needs the host keys it may be answered with")
		}
	}
	return sshKey(account, key, pinnedHostKeys(hostKeys))
}

// pinnedHostKeys accepts exactly the keys given, whatever host name was dialled:
// the caller already chose the keys BY host.
func pinnedHostKeys(keys []gossh.PublicKey) gossh.HostKeyCallback {
	return func(hostname string, _ net.Addr, presented gossh.PublicKey) error {
		for _, k := range keys {
			if bytes.Equal(k.Marshal(), presented.Marshal()) {
				return nil
			}
		}
		return fmt.Errorf("%s presented a %s host key that is not one pinned for it (%s)",
			hostname, presented.Type(), gossh.FingerprintSHA256(presented))
	}
}

func sshKey(account, key string, hostKey gossh.HostKeyCallback) Option {
	return func(c *Repository) error {
		if account == "" {
			return errors.New("an ssh key needs the account it authenticates as")
		}
		_, err := gossh.ParsePrivateKey([]byte(key))
		if err != nil {
			return fmt.Errorf("parsing private key failed with %w", err)
		}

		auth, err := ssh.NewPublicKeys(account, []byte(key), "")
		if err != nil {
			return fmt.Errorf("adding SSH Key failed with %w", err)
		}
		auth.HostKeyCallbackHelper = ssh.HostKeyCallbackHelper{HostKeyCallback: hostKey}

		c.auth = auth
		return nil
	}
}

// Root sets the directory the repository lives in.
func Root(root string) Option {
	return func(c *Repository) error {
		c.root = root
		return nil
	}
}

// Branch sets the branch to check out.
func Branch(branch string) Option {
	return func(c *Repository) error {
		c.branches = []string{branch}
		c.usingSpecificBranch = true
		return nil
	}
}
