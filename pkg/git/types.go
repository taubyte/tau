package git

import (
	"context"
	"io"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

/* Repository represents a repository.
 *
 * ctx: The context to use.
 * repo: The repository.
 * url: The url to the repository.
 * auth: The authentication to use.
 * root: The root of the repository.
 * branches: The branches to use.
 * usingSpecifcBranch: If a specific branch is used.
 */
type Repository struct {
	ctx                 context.Context
	repo                *git.Repository
	url                 string
	output              io.Writer
	auth                transport.AuthMethod
	root                string
	branches            []string
	usingSpecificBranch bool
}

func (c *Repository) Repo() *git.Repository {
	return c.repo
}
