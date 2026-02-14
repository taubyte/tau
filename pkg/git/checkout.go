package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

func (c *Repository) Checkout(branchName string) error {
	branchRef := plumbing.NewBranchReferenceName(branchName)

	// Ensure branch config exists (needed for both empty and non-empty repo)
	err := c.repo.CreateBranch(&config.Branch{Name: branchName, Remote: "origin", Merge: branchRef})
	if err != git.ErrBranchExists && err != nil {
		return fmt.Errorf("creating branch %s, failed with: %v", branchName, err)
	}

	// Detect empty repo: no commits means we only set HEAD and return (no symref to origin, no worktree checkout)
	hasCommits := false
	if head, err := c.repo.Head(); err == nil {
		if _, err := c.repo.CommitObject(head.Hash()); err == nil {
			hasCommits = true
		}
	}
	if !hasCommits {
		headRef := plumbing.NewSymbolicReference(plumbing.HEAD, branchRef)
		if err := c.repo.Storer.SetReference(headRef); err != nil {
			return fmt.Errorf("setting HEAD to %s failed with: %v", branchName, err)
		}
		return nil
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return err
	}

	// https://github.com/go-git/go-git/issues/279#issuecomment-816359799
	remoteRef := plumbing.NewRemoteReferenceName("origin", branchName)
	newReference := plumbing.NewSymbolicReference(branchRef, remoteRef)
	err = c.repo.Storer.SetReference(newReference)
	if err != nil {
		return fmt.Errorf("setting reference to %s failed with: %v", remoteRef, err)
	}

	// Already on the target branch, no need to checkout
	head, err := c.repo.Head()
	if err == nil {
		if head.Name() == branchRef {
			return nil
		}
	}

	err = wt.Checkout(&git.CheckoutOptions{
		Branch: branchRef,
		Force:  true, // Force checkout to handle any unstaged changes (common after clone)
	})
	if err != nil {
		return fmt.Errorf("Checkout %s failed with: %v", branchName, err)
	}

	return nil
}
