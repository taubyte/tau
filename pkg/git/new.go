package git

import (
	"context"
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
func Info(format string, args ...interface{}) {
	pterm.EnableDebugMessages()
	pterm.Info.Printfln(format, args...)
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
		ctx: ctx,
	}

	for _, opt := range options {
		err = opt(c)
		if err != nil {
			return
		}
	}

	if c.ephemeral {
		err = c.handle_ephemeral()
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

	cloneURL := c.url
	if c.embedToken {
		cloneURL, err = embedGitToken(cloneURL, c.auth)
		if err != nil {
			return fmt.Errorf("embedding token failed with: %s", err)
		}
	}

	c.repo, err = git.PlainCloneContext(c.ctx, c.root, false, &git.CloneOptions{
		URL:      cloneURL,
		Progress: os.Stdout,
		Auth:     c.auth,
	})

	if err == git.ErrRepositoryAlreadyExists {
		err = nil
	} else if err == plumbing.ErrReferenceNotFound || err == transport.ErrEmptyRemoteRepository {
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

		if _, err = r.CreateRemote(&config.RemoteConfig{Name: git.DefaultRemoteName, URLs: []string{c.url}}); err != nil {
			return err
		}

		for _, branch := range c.branches {
			merge := plumbing.ReferenceName("refs/heads/" + branch)
			if err = r.CreateBranch(&config.Branch{Name: branch, Remote: git.DefaultRemoteName, Merge: merge}); err == nil {
				break
			}
		}
	}

	if err != nil {
		pterm.Error.Printf("Cloning %s failed with %s", c.url, err.Error())
		return err
	}

	pterm.Success.Printf("Cloning %s complete\n", c.url)
	c.i_cloned_it = true

	return nil
}
