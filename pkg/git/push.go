package git

import (
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

/* Push pushes the changes to the repository.
 *
 * Returns error if something goes wrong.
 */
func (c *Repository) Push() error {
	_, err := c.PushNew()
	return err
}

// PushNew is Push, and also says whether anything was sent: a remote that
// already has this commit gets no webhook, so no build follows.
func (c *Repository) PushNew() (bool, error) {
	head, err := c.repo.Head()
	if err != nil {
		return false, fmt.Errorf("getting HEAD failed: %w", err)
	}
	branchName := head.Name()
	if !branchName.IsBranch() {
		return false, fmt.Errorf("HEAD is not on a branch")
	}
	refSpec := config.RefSpec(branchName.String() + ":" + branchName.String())
	err = c.repo.PushContext(c.ctx, &git.PushOptions{
		Auth:     c.auth,
		RefSpecs: []config.RefSpec{refSpec},
	})
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("push failed: %w", err)
	}
	return true, nil
}
