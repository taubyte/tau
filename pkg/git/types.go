package git

import (
	"context"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

type user struct {
	name  string
	email string
}

/* Repository represents a repository.
 *
 * ctx: The context to use.
 * repo: The repository.
 * workdir: The working directory.
 * i_cloned_it: If I cloned the repository.
 * url: The url to the repository.
 * auth: The authentication to use.
 * root: The root of the repository.
 * ephemeral: If the repository is ephemeral.
 * ephemeralNoDelete: If the ephemeral repository should not be deleted.
 * user: The user to use.
 * branches: The branches to use.
 * usingSpecifcBranch: If a specific branch is used.
 */
type Repository struct {
	ctx                 context.Context
	repo                *git.Repository
	workDir             string
	i_cloned_it         bool
	url                 string
	auth                transport.AuthMethod
	root                string
	ephemeral           bool
	ephemeralNoDelete   bool
	user                user
	branches            []string
	usingSpecificBranch bool
	embedToken          bool
}

func (c *Repository) Repo() *git.Repository {
	return c.repo
}
