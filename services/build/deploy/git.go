package deploy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/taubyte/tau/pkg/forge"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/pkg/git/repokey"
)

// git.go is the Checkout a node actually runs: a real clone of a real
// repository, at the commit the push named.
//
// THE COMMIT IS CHECKED OUT, NOT THE BRANCH. A branch moves. By the time a job
// is claimed the branch may be two commits further on, and a deploy that read
// whatever it pointed at then would publish something no push ever asked for
// and record it under the sha of one that did.

// Credentials is how a private repository is read.
//
// A DEPLOY KEY IS PER REPOSITORY, which is the only shape that lets one cloud
// serve repositories belonging to people who do not know each other: a token
// with account scope read across a whole account, and one leaked from a build
// would be a key to every repository in it.
//
// nil means public repositories only, and a private one then fails at the
// clone with git's own message rather than silently building nothing.
type Credentials interface {
	// DeployKey is the repository's private key, in PEM. An empty string with
	// no error means this repository needs none.
	DeployKey(ctx context.Context, repositoryKey string) (string, error)
}

// Clone is a Checkout over pkg/git.
type Clone struct {
	credentials Credentials

	// address turns a repository key into where to clone it from, and as whom.
	//
	// IT IS CLOUD POLICY AND NOT REPOSITORY FACT. A key is a host and a path
	// under it and says nothing about transport, so whether this cloud reaches
	// a host over https or ssh — or reaches it at all — is the operator's
	// answer and not the pushed payload's. The ssh ACCOUNT is the forge's: see
	// Forged.
	address func(repositoryKey string) (forge.Address, error)
}

var _ Checkout = (*Clone)(nil)

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

// WithAddress is how this cloud reaches a repository. Default HTTPS.
func WithAddress(address func(repositoryKey string) (forge.Address, error)) CloneOption {
	return func(c *Clone) {
		if address != nil {
			c.address = address
		}
	}
}

// HTTPS is `https://<key>.git`, which reads what is public and nothing else.
func HTTPS(repositoryKey string) (forge.Address, error) {
	key, err := parse(repositoryKey)
	if err != nil {
		return forge.Address{}, err
	}
	return forge.Address{URL: "https://" + key.String() + ".git"}, nil
}

// Forged is the address the forge serving a key's host gives it, over the
// given protocol. SSH is what a deploy key needs, and the account it
// authenticates as is the driver's to say — not a `git@` spelled here.
//
// ASKED ANONYMOUSLY, because an address is a host fact and not a permission:
// the credential a clone uses is the repository's deploy key, handed to git,
// never a caller's token.
func Forged(forges *forge.Registry, over forge.Protocol) func(repositoryKey string) (forge.Address, error) {
	return func(repositoryKey string) (forge.Address, error) {
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

func parse(repositoryKey string) (repokey.Key, error) {
	key, err := repokey.Parse("https://" + repositoryKey)
	if err != nil {
		return repokey.Key{}, fmt.Errorf("deploy: %q is not a repository: %w", repositoryKey, err)
	}
	return key, nil
}

// Checkout clones into a directory this process owns and removes.
func (c *Clone) Checkout(ctx context.Context, repositoryKey, branch, commit string) (string, func(), error) {
	address, err := c.address(repositoryKey)
	if err != nil {
		return "", nil, err
	}

	// ITS OWN DIRECTORY, REMOVED WITH IT. A build reads a stranger's repository;
	// leaving it next to the node's data is how one build sees another's.
	root, err := os.MkdirTemp("", "tau-deploy-")
	if err != nil {
		return "", nil, err
	}
	release := func() { os.RemoveAll(root) }

	options := []git.Option{
		git.URL(address.URL),
		git.Root(root),
		git.Branch(branch),
		git.Output(io.Discard),
	}
	if c.credentials != nil {
		key, err := c.credentials.DeployKey(ctx, repositoryKey)
		if err != nil {
			release()
			return "", nil, fmt.Errorf("deploy: reading %s's key: %w", repositoryKey, err)
		}
		if key != "" {
			// A KEY OVER HTTPS AUTHENTICATES NOTHING, and git would say so as a
			// failed clone of what reads as a missing repository.
			if address.SSHAccount == "" {
				release()
				return "", nil, fmt.Errorf("deploy: %s has a deploy key and is reached by %q, which is not ssh", repositoryKey, address.URL)
			}
			options = append(options, git.SSHKeyAs(address.SSHAccount, key))
		}
	}

	repo, err := git.New(ctx, options...)
	if err != nil {
		release()
		return "", nil, err
	}

	if err := checkoutCommit(repo.Repo(), commit); err != nil {
		release()
		return "", nil, err
	}

	return repo.Dir(), release, nil
}

// checkoutCommit moves the worktree to one exact commit.
//
// A COMMIT THE CLONE DOES NOT HAVE IS AN ERROR AND NOT A FALLBACK. It means the
// branch was force-pushed or rewritten between the webhook and the claim, and
// building the branch head instead would deploy a commit nobody pushed.
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
