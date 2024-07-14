package methods

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
)

func GetRepositoryPath(gitProvider, repoId, projectId string) (*RepositoryPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a repository path")
	}

	return &RepositoryPath{
		value: []string{string(RepositoryPathVariable), gitProvider, repoId, projectId},
	}, nil
}

func (r *RepositoryPath) Type() *common.TnsPath {
	return common.NewTnsPath(append(r.value, TypePathVariable.String()))
}

func (r *RepositoryPath) Resource(resourceId string) *common.TnsPath {
	return common.NewTnsPath(append(r.value, ResourcePathVariable.String(), resourceId))
}

func (r *RepositoryPath) AllResources() *common.TnsPath {
	return common.NewTnsPath(append(r.value, ResourcePathVariable.String()))
}
