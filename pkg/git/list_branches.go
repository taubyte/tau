package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

/*
ListBranches will return a list of branches for the repository

fetch true will search remote origin to gather all branches
fetch false will search .git/config to gather branches
*/
func (r *Repository) ListBranches(fetch bool) (branches []string, fetchErr error, err error) {
	if fetch {
		return r.fetchAndListBranches()
	}

	branchRef, err := r.repo.Branches()
	if err != nil {
		return nil, fetchErr, fmt.Errorf("listing branches for repository: `%s` failed with: %s", r.url, err)
	}

	branches = make([]string, 0)
	err = branchRef.ForEach(func(r *plumbing.Reference) error {
		branches = append(branches, r.Name().Short())
		return nil
	})
	if err != nil {
		return nil, fetchErr, fmt.Errorf("branchRef.ForEach() for repository: `%s` failed with: %s", r.url, err)
	}

	return branches, fetchErr, nil
}

func (r *Repository) fetchAndListBranches() (branches []string, fetchErr error, err error) {
	fetchErr = r.Fetch()

	rem, err := r.repo.Remote("origin")
	if err != nil {
		return nil, fetchErr, fmt.Errorf("getting remote origin for repository: `%s` failed with: %s", r.url, err)
	}

	remoteLister, err := rem.List(&git.ListOptions{
		Auth: r.auth,
	})
	if err != nil {
		return nil, fetchErr, fmt.Errorf("listing origin references for repository: `%s` failed with: %s", r.url, err)
	}

	branches = make([]string, 0)
	for _, branch := range remoteLister {
		if branch.Name().IsBranch() {
			branches = append(branches, branch.Name().Short())
		}
	}

	return
}
