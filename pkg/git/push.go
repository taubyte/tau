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
	head, err := c.repo.Head()
	if err != nil {
		return fmt.Errorf("getting HEAD failed: %w", err)
	}
	branchName := head.Name()
	if !branchName.IsBranch() {
		return fmt.Errorf("HEAD is not on a branch")
	}
	refSpec := config.RefSpec(branchName.String() + ":" + branchName.String())
	err = c.repo.PushContext(c.ctx, &git.PushOptions{
		Auth:     c.auth,
		RefSpecs: []config.RefSpec{refSpec},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return fmt.Errorf("push failed: %w", err)
	}
	return nil
}
