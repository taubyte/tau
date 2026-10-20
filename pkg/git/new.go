package git

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/pterm/pterm"
)

/* Info uses pterm to display info messages.
 *
 * format: The format to use.
 * args: The arguments to use.
 */
func Info(args ...interface{}) {
	pterm.EnableDebugMessages()
	pterm.Info.Println(args...)
}

/*
New creates a new repository.
  - ctx: The context to use.
  - options: The options to use.
    *
  - Returns the repository and error if something goes wrong.
*/
func New(ctx context.Context, options ...Option) (c *Repository, err error) {
	c = &Repository{
		ctx:    ctx,
		output: os.Stdout,
	}

	for _, opt := range options {
		err = opt(c)
		if err != nil {
			return
		}
	}

	err = c.open_or_clone()
	if err != nil {
		return
	}

	if c.usingSpecificBranch {
		err = c.Checkout(c.branches[0])
		if err != nil {
			return
		}
	}

	return
}

func (c *Repository) open_or_clone() error {
	var err error

	if !c.usingSpecificBranch {
		c.branches = []string{"main", "master"}
	}

	c.repo, err = git.PlainOpen(c.root)
	if err != nil {
		return c.clone()
	}

	return nil
}

func (c *Repository) clone() (err error) {
	Info("Cloning from " + c.url + " on branch " + c.branches[0] + " into " + c.root + "\n")

	// ONE TRANSPORT, ONE ATTEMPT. The caller chose the address and the
	// credential together; a clone that fails is reported as it failed, never
	// retried anonymously over another transport that might read something else.
	c.repo, err = git.PlainCloneContext(c.ctx, c.root, false, &git.CloneOptions{
		URL:      c.url,
		Progress: c.output,
		Auth:     c.auth,
	})

	if err == git.ErrRepositoryAlreadyExists {
		err = nil
	} else if errors.Is(err, plumbing.ErrReferenceNotFound) || errors.Is(err, transport.ErrEmptyRemoteRepository) {
		defer func() {
			if err != nil {
				_ = os.RemoveAll(c.root)
			}
		}()

		var r *git.Repository
		r, err = git.PlainInit(c.root, false /* isBare */)
		if err != nil {
			return err
		}

		if _, err = r.CreateRemote(&config.RemoteConfig{
			Name:  git.DefaultRemoteName,
			URLs:  []string{c.url},
			Fetch: []config.RefSpec{config.RefSpec(fmt.Sprintf(config.DefaultFetchRefSpec, git.DefaultRemoteName))},
		}); err != nil {
			return err
		}

		// Create branch config and set HEAD so first commit/push uses main (not master)
		mainBranch := c.branches[0]
		merge := plumbing.ReferenceName("refs/heads/" + mainBranch)
		if err = r.CreateBranch(&config.Branch{Name: mainBranch, Remote: git.DefaultRemoteName, Merge: merge}); err != nil {
			return err
		}
		headRef := plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.ReferenceName("refs/heads/"+mainBranch))
		if err = r.Storer.SetReference(headRef); err != nil {
			return err
		}

		c.repo = r
	}

	if err != nil {
		pterm.Error.Printf("Cloning %s failed with %s", c.url, err.Error())
		return err
	}

	pterm.Success.Printf("Cloning %s complete\n", c.url)

	return nil
}
