package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
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

	// When token is passed and remote is SSH, switch origin to HTTPS so push/fetch use the token. Do not fail open.
	if err := c.switchOriginToHTTPSIfTokenAndSSH(); err != nil {
		Info("switchOriginToHTTPSIfTokenAndSSH: " + err.Error() + "\n")
	}

	return nil
}

func (c *Repository) clone() (err error) {
	Info("Cloning from " + c.url + " on branch " + c.branches[0] + " into " + c.root + "\n")

	if c.embedToken {
		var cloneURL string
		cloneURL, err = embedGitToken(c.url, c.auth)
		if err != nil {
			return fmt.Errorf("embedding token failed with: %s", err)
		}

		c.repo, err = git.PlainCloneContext(c.ctx, c.root, false, &git.CloneOptions{
			URL:      cloneURL,
			Progress: c.output,
		})
	} else if c.auth != nil {
		c.repo, err = git.PlainCloneContext(c.ctx, c.root, false, &git.CloneOptions{
			URL:      c.url,
			Progress: c.output,
			Auth:     c.auth,
		})
	} else {
		c.repo, err = git.PlainCloneContext(c.ctx, c.root, false, &git.CloneOptions{
			URL:      c.url,
			Progress: c.output,
		})
	}

	if err != nil && strings.Contains(err.Error(), "ssh: unable to authenticate") {
		// repo might be public or we're in dev/test mode. try to clone with https
		c.repo, err = git.PlainCloneContext(c.ctx, c.root, false, &git.CloneOptions{
			URL:      ConvertSSHToHTTPS(c.url),
			Progress: c.output,
		})
	}

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
	c.i_cloned_it = true

	return nil
}

// switchOriginToHTTPSIfTokenAndSSH sets origin remote URL to HTTPS when c has token auth and origin is SSH.
func (c *Repository) switchOriginToHTTPSIfTokenAndSSH() error {
	if c.repo == nil {
		return nil
	}
	if _, ok := c.auth.(*http.BasicAuth); !ok {
		return nil
	}
	rem, err := c.repo.Remote(git.DefaultRemoteName)
	if err != nil || rem == nil {
		return nil
	}
	urls := rem.Config().URLs
	if len(urls) == 0 {
		return nil
	}
	url := urls[0]
	if !strings.HasPrefix(url, "git@") || !strings.Contains(url, ":") {
		return nil
	}
	newURL := ConvertSSHToHTTPS(url)
	cfg, err := c.repo.Config()
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}
	origin, ok := cfg.Remotes[git.DefaultRemoteName]
	if !ok || origin == nil || len(origin.URLs) == 0 {
		return nil
	}
	for i := range origin.URLs {
		origin.URLs[i] = newURL
	}
	if err := c.repo.SetConfig(cfg); err != nil {
		return fmt.Errorf("set config: %w", err)
	}
	return nil
}
