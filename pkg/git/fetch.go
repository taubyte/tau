// Currently runs into failed with object not found

package git

import (
	"fmt"

	"github.com/go-git/go-git/v5"
)

/* Fetch fetches the changes from the repository.
 *
 * Returns error if something goes wrong.
 */
func (c *Repository) Fetch() error {
	_, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("fetching worktree when pulling failed with %s", err.Error())
	}

	err = c.repo.Fetch(&git.FetchOptions{
		Force: true,
		Depth: 1,
		Auth:  c.auth,
	})
	if err != nil {
		return fmt.Errorf("fetching from repo failed with: %s", err)
	}

	return nil
}
