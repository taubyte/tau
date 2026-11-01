package git

import (
	"context"
	"io"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
)

// Repository is a git repository on disk.
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
