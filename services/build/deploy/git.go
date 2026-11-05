package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/pkg/git/repokey"
	"github.com/taubyte/tau/services/build/run"
)

// git.go is the Checkout a node runs: a real clone at the pushed commit, never the
// branch head.

// Credentials is how a private repository is read: a per-repository deploy key,
// never an account-scoped token. nil means public repositories only.
type Credentials interface {
	// DeployKey is the repository's private key, in PEM. An empty string with
	// no error means this repository needs none.
	DeployKey(ctx context.Context, repositoryKey string) (string, error)
}

// Clone is a Checkout over pkg/git.
type Clone struct {
	credentials Credentials

	// address turns a repository key into a clone address over a protocol. The
	// protocol follows the credential: ssh with a deploy key, anonymous https
	// without.
	address func(repositoryKey string, over forge.Protocol) (forge.Address, error)

	// hostKeys are the ssh host keys a forge host may answer with. A clone over
	// ssh to a host with none is refused: see git.SSHKeyAs.
	hostKeys func(host string) []gossh.PublicKey
}

var (
	_ Checkout      = (*Clone)(nil)
	_ run.Confirmer = (*Clone)(nil)
)

// NewClone builds one. A nil credentials reads public repositories only.
func NewClone(credentials Credentials, opts ...CloneOption) *Clone {
	c := &Clone{credentials: credentials, address: HTTPS}
	for _, apply := range opts {
		apply(c)
	}
	return c
}

// CloneOption configures a Clone.
type CloneOption func(*Clone)

// WithAddress is how this cloud reaches a repository over a protocol. Default
// HTTPS, which reaches nothing over ssh.
func WithAddress(address func(repositoryKey string, over forge.Protocol) (forge.Address, error)) CloneOption {
	return func(c *Clone) {
		if address != nil {
			c.address = address
		}
	}
}

// WithHostKeys is the ssh host keys each forge host may answer a clone with,
// keyed by the host a repository key names.
func WithHostKeys(keys func(host string) []gossh.PublicKey) CloneOption {
	return func(c *Clone) {
		if keys != nil {
			c.hostKeys = keys
		}
	}
}

// HTTPS is `https://<key>.git`, which reads what is public and nothing else.
// Asked for ssh it refuses: it knows no forge, so no ssh address or account.
func HTTPS(repositoryKey string, over forge.Protocol) (forge.Address, error) {
	key, err := parse(repositoryKey)
	if err != nil {
		return forge.Address{}, err
	}
	if over != forge.HTTPS {
		return forge.Address{}, fmt.Errorf("deploy: %s has a deploy key, and no forge is bound to give it an ssh address", repositoryKey)
	}
	return forge.Address{URL: "https://" + key.String() + ".git"}, nil
}

// Forged is the address the forge serving a key's host gives it: over ssh with the
// driver's account, over https its anonymous clone URL. Asked anonymously; the
// clone's credential is the deploy key.
func Forged(forges *forge.Registry) func(repositoryKey string, over forge.Protocol) (forge.Address, error) {
	return func(repositoryKey string, over forge.Protocol) (forge.Address, error) {
		key, err := parse(repositoryKey)
		if err != nil {
			return forge.Address{}, err
		}
		session, err := forges.For(key, forge.Anonymous())
		if err != nil {
			return forge.Address{}, fmt.Errorf("deploy: %w", err)
		}
		return session.CloneAddress(key, over)
	}
}

// Mirrored reaches a repository the file:// directory base holds a folder for,
// its key joined on, read with no credential, and every other through otherwise: a
// development cloud's mirror of the repositories it was handed.
func Mirrored(base string, otherwise func(repositoryKey string, over forge.Protocol) (forge.Address, error)) func(repositoryKey string, over forge.Protocol) (forge.Address, error) {
	root := strings.TrimSuffix(strings.TrimPrefix(base, "file://"), "/")
	return func(repositoryKey string, over forge.Protocol) (forge.Address, error) {
		key, err := parse(repositoryKey)
		if err != nil {
			return forge.Address{}, err
		}
		at := root + "/" + key.String()
		if info, err := os.Stat(at); err == nil && info.IsDir() {
			return forge.Address{URL: "file://" + at}, nil
		}
		return otherwise(repositoryKey, over)
	}
}

func parse(repositoryKey string) (repokey.Key, error) {
	key, err := repokey.Parse("https://" + repositoryKey)
	if err != nil {
		return repokey.Key{}, fmt.Errorf("deploy: %q is not a repository: %w", repositoryKey, err)
	}
	return key, nil
}

// Checkout clones into a directory this process owns and removes.
func (c *Clone) Checkout(ctx context.Context, repositoryKey, branch, commit string) (string, func(), error) {
	options, deployKey, err := c.reach(ctx, repositoryKey)
	if err != nil {
		return "", nil, err
	}
	options = append(options, git.Branch(branch), git.Output(io.Discard))

	root, err := os.MkdirTemp("", "tau-deploy-")
	if err != nil {
		return "", nil, err
	}
	release := func() { os.RemoveAll(root) }

	repo, err := git.New(ctx, append(options, git.Root(root))...)
	if err != nil {
		release()
		// NO KEY MEANS PUBLIC OR NOTHING. A keyless clone the forge refuses is
		// either a repository that does not exist or a private one nobody
		// linked; an anonymous 404 cannot tell them apart, so both are named.
		// No other transport is tried, because none holds a credential either.
		if deployKey == "" && refused(err) {
			return "", nil, fmt.Errorf("deploy: %s was not found or is not publicly readable; if it is private, run tau link: %w", repositoryKey, err)
		}
		return "", nil, err
	}

	if err := checkoutCommit(repo.Repo(), commit); err != nil {
		release()
		return "", nil, err
	}

	return repo.Dir(), release, nil
}

// reach is how repositoryKey is fetched: its address, and its deploy key if set.
func (c *Clone) reach(ctx context.Context, repositoryKey string) ([]git.Option, string, error) {
	key, err := parse(repositoryKey)
	if err != nil {
		return nil, "", err
	}

	var deployKey string
	if c.credentials != nil {
		deployKey, err = c.credentials.DeployKey(ctx, repositoryKey)
		if err != nil {
			return nil, "", fmt.Errorf("deploy: reading %s's key: %w", repositoryKey, err)
		}
	}

	over := forge.HTTPS
	if deployKey != "" {
		over = forge.SSH
	}
	address, err := c.address(repositoryKey, over)
	if err != nil {
		return nil, "", err
	}

	options := []git.Option{git.URL(address.URL)}
	if deployKey != "" {
		if address.SSHAccount == "" {
			return nil, "", fmt.Errorf("deploy: %s has a deploy key and is reached by %q, which is not ssh", repositoryKey, address.URL)
		}
		var pinned []gossh.PublicKey
		if c.hostKeys != nil {
			pinned = c.hostKeys(key.Host)
		}
		if len(pinned) == 0 {
			return nil, "", fmt.Errorf("deploy: no ssh host key is pinned for %s, and a clone that accepts any host can be answered by anyone", repositoryKey)
		}
		options = append(options, git.SSHKeyAs(address.SSHAccount, deployKey, pinned))
	}
	return options, deployKey, nil
}

// Confirm asks the repository itself, over its deploy key, whether ref is at
// commit — so a push is built only for what the owner's repository holds,
// whatever a delivery claimed.
func (c *Clone) Confirm(ctx context.Context, repositoryKey, ref, commit string) error {
	options, _, err := c.reach(ctx, repositoryKey)
	if err != nil {
		return err
	}
	refs, err := git.ListRemote(ctx, options...)
	if err != nil {
		return fmt.Errorf("deploy: listing %s: %w", repositoryKey, err)
	}
	for _, r := range refs {
		if r.Name().String() == ref && r.Hash().String() == commit {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %s %s", run.ErrNotAtCommit, repositoryKey, ref, short(commit))
}

func refused(err error) bool {
	return errors.Is(err, transport.ErrAuthenticationRequired) ||
		errors.Is(err, transport.ErrAuthorizationFailed) ||
		errors.Is(err, transport.ErrRepositoryNotFound)
}

// checkoutCommit moves the worktree to one exact commit; a missing commit (force
// push) is an error, never a fallback to the head.
func checkoutCommit(repo *gogit.Repository, commit string) error {
	if commit == "" {
		return errors.New("deploy: the push names no commit")
	}

	hash := plumbing.NewHash(commit)
	if hash.IsZero() {
		return fmt.Errorf("deploy: %q is not a commit", commit)
	}
	if _, err := repo.CommitObject(hash); err != nil {
		return fmt.Errorf("deploy: %s is not in this clone; the branch was rewritten between the push and the build: %w",
			short(commit), err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return err
	}
	return worktree.Checkout(&gogit.CheckoutOptions{Hash: hash})
}
