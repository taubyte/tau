package common

import "github.com/google/go-github/v32/github"

// The following interfaces are used to abstract from the underlying git provider
// Currently only github is supported, but in the future we plan to support more

// Client is the interface for the git client
type Client interface {
	CreateRepository(name string, description string, private bool) (Repository, error)
	GetByID(id string) (Repository, error)
	GetByName(owner, name string) (Repository, error)
	ListRepositories() ([]Repository, error)
	ReadConfig(owner, repo string) (*ProjectConfig, error)

	// Used to extract the inner github client
	GithubTODO() (*github.Client, error)
}

// Repository is the interface for the git repository
type Repository interface {
	Get() RepositoryGetter

	// Used to extract the inner github.Repository type
	// Note: this is a temporary solution, if you need to use this function
	// you should submit a PR to add the function to the interface
	GithubTODO() (*github.Repository, error)
}

// RepositoryGetter is an interface for getting information about a repository
type RepositoryGetter interface {
	ID() string
	Name() string
	FullName() string
	Private() bool
}

// ProjectConfig is read from the config.yaml file in the configuration repository
type ProjectConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Notification struct {
		Email string `json:"email"`
	} `json:"notification"`
}
