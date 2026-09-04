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
	"github.com/spf13/afero"

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

	// url turns a repository key into something to clone.
	//
	// IT IS CLOUD POLICY AND NOT REPOSITORY FACT. A key is a host and a path
	// under it and says nothing about transport, so whether this cloud reaches
	// github.com over https or ssh — or reaches an internal mirror at all — is
	// the operator's answer and not the pushed payload's.
	url func(repositoryKey string) (string, error)
}

var _ Checkout = (*Clone)(nil)

// NewClone builds one. A nil credentials reads public repositories only.
func NewClone(credentials Credentials, opts ...CloneOption) *Clone {
	c := &Clone{credentials: credentials, url: HTTPS}
	for _, apply := range opts {
		apply(c)
	}
	return c
}

// CloneOption configures a Clone.
type CloneOption func(*Clone)

// WithURL is how this cloud reaches a repository. Default HTTPS.
func WithURL(url func(repositoryKey string) (string, error)) CloneOption {
	return func(c *Clone) {
		if url != nil {
			c.url = url
		}
	}
}

// HTTPS is `https://<key>.git`.
func HTTPS(repositoryKey string) (string, error) {
	key, err := repokey.Parse("https://" + repositoryKey)
	if err != nil {
		return "", fmt.Errorf("deploy: %q is not a repository: %w", repositoryKey, err)
	}
	return "https://" + key.String() + ".git", nil
}

// SSH is `git@<host>:<owner>/<name>.git`, which is what a deploy key needs.
func SSH(repositoryKey string) (string, error) {
	key, err := repokey.Parse("https://" + repositoryKey)
	if err != nil {
		return "", fmt.Errorf("deploy: %q is not a repository: %w", repositoryKey, err)
	}
	return "git@" + key.Host + ":" + strings.TrimPrefix(key.FullName(), "/") + ".git", nil
}

// Checkout clones into a directory this process owns and removes.
func (c *Clone) Checkout(ctx context.Context, repositoryKey, branch, commit string) (afero.Fs, string, func(), error) {
	url, err := c.url(repositoryKey)
	if err != nil {
		return nil, "", nil, err
	}

	// ITS OWN DIRECTORY, REMOVED WITH IT. A build reads a stranger's repository;
	// leaving it next to the node's data is how one build sees another's.
	root, err := os.MkdirTemp("", "tau-deploy-")
	if err != nil {
		return nil, "", nil, err
	}
	release := func() { os.RemoveAll(root) }

	options := []git.Option{
		git.URL(url),
		git.Root(root),
		git.Branch(branch),
		git.Output(io.Discard),
	}
	if c.credentials != nil {
		key, err := c.credentials.DeployKey(ctx, repositoryKey)
		if err != nil {
			release()
			return nil, "", nil, fmt.Errorf("deploy: reading %s's key: %w", repositoryKey, err)
		}
		if key != "" {
			options = append(options, git.SSHKey(key))
		}
	}

	repo, err := git.New(ctx, options...)
	if err != nil {
		release()
		return nil, "", nil, err
	}

	if err := checkoutCommit(repo.Repo(), commit); err != nil {
		release()
		return nil, "", nil, err
	}

	return afero.NewOsFs(), repo.Dir(), release, nil
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
