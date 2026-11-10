package git

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// Cloned tells whether New had to clone (false: an existing clone was fetched).
func (c *Repository) Cloned() bool { return c.i_cloned_it }

func (c *Repository) fetch(refspec string) error {
	err := c.repo.FetchContext(c.ctx, &git.FetchOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []config.RefSpec{config.RefSpec(refspec)},
		Depth:      1,
		Force:      true,
		Auth:       c.auth,
		Progress:   c.output,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return err
	}
	return nil
}

// refresh fetches the branch tip into the opened clone; pin moves the tree.
func (c *Repository) refresh() error {
	branch := c.branches[0]
	return c.fetch(fmt.Sprintf("+refs/heads/%s:%s", branch, plumbing.NewRemoteReferenceName(git.DefaultRemoteName, branch)))
}

// pin leaves the tree exactly at the target commit (the branch tip when none
// is set or it cannot be fetched): every file but .git is dropped first, so
// nothing untracked or ignored survives from an earlier build. It runs after
// Checkout, which would otherwise move the tree back to the tip.
func (c *Repository) pin() error {
	target, err := c.repo.ResolveRevision(plumbing.Revision(plumbing.NewRemoteReferenceName(git.DefaultRemoteName, c.branches[0])))
	if err != nil {
		return err
	}
	if c.commit != "" && c.commit != target.String() {
		h := plumbing.NewHash(c.commit)
		if _, err := c.repo.CommitObject(h); err != nil {
			// A depth-1 fetch of the branch only has its tip: ask for the commit itself.
			if err := c.fetch(fmt.Sprintf("+%s:refs/tau/pin", c.commit)); err != nil {
				fmt.Fprintf(c.output, "commit %s not fetched (%s), building the tip of %s\n", c.commit, err, c.branches[0])
			}
		}
		if _, err := c.repo.CommitObject(h); err == nil {
			target = &h
		}
	}

	ents, err := os.ReadDir(c.root)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.Name() != ".git" {
			if err = os.RemoveAll(filepath.Join(c.root, e.Name())); err != nil {
				return err
			}
		}
	}
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}
	return wt.Reset(&git.ResetOptions{Commit: *target, Mode: git.HardReset})
}
