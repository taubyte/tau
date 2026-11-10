package projectLib

import (
	"context"

	httpClient "github.com/taubyte/tau/clients/http/auth"
	"github.com/taubyte/tau/pkg/git"
	"github.com/taubyte/tau/tools/tau/config"
	projectI18n "github.com/taubyte/tau/tools/tau/i18n/project"
)

type repositoryHandler struct {
	projectName string
	project     *httpClient.Project // when set, it need not be looked up by name

	config *git.Repository
	code   *git.Repository
}

func Repository(projectName string) RepositoryHandler {
	return &repositoryHandler{projectName: projectName}
}

// repositoryOf is Repository for a project already at hand: finding one by
// name lists every project the caller has.
func repositoryOf(project *httpClient.Project) RepositoryHandler {
	return &repositoryHandler{projectName: project.Name, project: project}
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
		context.Background(),
		_ops...,
	)
}
