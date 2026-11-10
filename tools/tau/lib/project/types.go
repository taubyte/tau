package projectLib

import (
	httpClient "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/config"
)

type Project struct {
	Id          string
	Name        string
	Description string
	Public      bool

	// Account / Plan pin the project to a tau Account + Plan on the active
	// profile's cloud. Both empty = unbound. Both-or-neither enforced by
	// projectLib.BindingFlags.
	Account string
	Plan    string

	// Resume is set when this name is a project whose creation never finished:
	// New picks it up instead of creating repositories.
	Resume *httpClient.Project
}

type ProjectRepository interface {
	Config() (*git.Repository, error)
	Code() (*git.Repository, error)
	CurrentBranch() (string, error)
}

type RepositoryHandler interface {
	Open() (ProjectRepository, error)
	Clone(tauProject config.Project, embedToken bool) (ProjectRepository, error)
}
