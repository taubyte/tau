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

// refresh fetches the branch into the opened clone and leaves the tree exactly
// at the target commit: every file but .git is dropped first, so nothing
// untracked or ignored survives from an earlier build.
func (c *Repository) refresh() error {
	branch := c.branches[0]
	remote := plumbing.NewRemoteReferenceName(git.DefaultRemoteName, branch)
	err := c.repo.FetchContext(c.ctx, &git.FetchOptions{
		RemoteName: git.DefaultRemoteName,
		RefSpecs:   []config.RefSpec{config.RefSpec(fmt.Sprintf("+refs/heads/%s:%s", branch, remote))},
		Depth:      1,
		Force:      true,
		Auth:       c.auth,
		Progress:   c.output,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return err
	}

	target, err := c.repo.ResolveRevision(plumbing.Revision(remote))
	if err != nil {
		return err
	}
	if c.commit != "" {
		h := plumbing.NewHash(c.commit)
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
