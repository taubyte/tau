package git

import (
	"fmt"

	"github.com/google/go-github/v32/github"
	"github.com/taubyte/odo/clients/http/auth/git/common"
)

type repository struct {
	*github.Repository
}

// GithubTODO is a temporary function to extract the inner github client
func (r *repository) GithubTODO() (*github.Repository, error) {
	return r.Repository, nil
}

type getter struct {
	*github.Repository
}

// Get returns a common.RepositoryGetter for extracting information about the repository
func (r *repository) Get() common.RepositoryGetter {
	return &getter{r.Repository}
}

// ID returns a string of repository id
func (g *getter) ID() string {
	return fmt.Sprintf("%d", g.GetID())
}

// Name returns the name of the repository
func (g *getter) Name() string {
	return g.GetName()
}

// FullName returns the `user/repository` fullname of the repository
func (g *getter) FullName() string {
	return g.GetFullName()
}

// Private returns a boolean indicating if the repository is private
func (g *getter) Private() bool {
	return g.GetPrivate()
}
