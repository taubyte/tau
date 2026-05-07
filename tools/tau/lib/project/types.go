package projectLib

import (
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
