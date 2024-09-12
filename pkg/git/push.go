package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
)

/* Push pushes the changes to the repository.
 *
 * Returns error if something goes wrong.
 */
func (c *Repository) Push() error {
	err := c.repo.PushContext(c.ctx, &git.PushOptions{
		Auth: c.auth,
	})
	if err != nil {
		return fmt.Errorf("Push failed with %s", err.Error())
	}
	return nil
}
