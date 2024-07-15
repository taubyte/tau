package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

func (c *Repository) Checkout(branchName string) error {
	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	// https://github.com/go-git/go-git/issues/279#issuecomment-816359799
	branchRef := plumbing.NewBranchReferenceName(branchName)
	remoteRef := plumbing.NewRemoteReferenceName("origin", branchName)
	err = c.repo.CreateBranch(&config.Branch{Name: branchName, Remote: "origin", Merge: branchRef})
	if err != git.ErrBranchExists && err != nil {
		return fmt.Errorf("creating branch %s, failed with: %v", branchName, err)
	}

	newReference := plumbing.NewSymbolicReference(branchRef, remoteRef)
	err = c.repo.Storer.SetReference(newReference)
	if err != nil {
		return fmt.Errorf("setting reference to %s failed with: %v", remoteRef, err)
	}

	err = wt.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
	})
	if err != nil {
		return fmt.Errorf("Checkout %s failed with: %v", branchName, err)
	}

	return nil
}
