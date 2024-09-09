package projectLib

import (
	"github.com/taubyte/tau/pkg/git"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
	"github.com/taubyte/tau/tools/tau/singletons/config"
	"github.com/taubyte/tau/tools/tau/states"
)

type repositoryHandler struct {
	projectName string

	config *git.Repository
	code   *git.Repository
}

func Repository(projectName string) RepositoryHandler {
	return &repositoryHandler{projectName: projectName}
}

func (h *repositoryHandler) Config() (*git.Repository, error) {
	if h.config != nil {
		return h.config, nil
	}

	return nil, projectI18n.ErrorConfigRepositoryNotFound
}

func (h *repositoryHandler) Code() (*git.Repository, error) {
	if h.code != nil {
		return h.code, nil
	}

	return nil, projectI18n.ErrorCodeRepositoryNotFound
}

func (h *repositoryHandler) openOrClone(profile config.Profile, loc string, ops ...git.Option) (*git.Repository, error) {
	_ops := []git.Option{
		git.Root(loc),
		git.Author(profile.GitUsername, profile.GitEmail),
	}

	// TODO branch this breaks stuff
	// Only pass branch if it is defined
	// if len(h.branch) > 0 {
	//     _ops = append(_ops, git.Branch(h.branch))
	// }

	// Passed in ops go at the end so they can override the default options above
	_ops = append(_ops, ops...)

	return git.New(
		states.Context,
		_ops...,
	)
}
