package methods

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
)

func IndexValue(branch, projectId, appId, resourceId string, resourceType common.PathVariable) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a TNS key")
	}

	if len(resourceId) == 0 {
		return nil, errors.New("resource id is required for creating a TNS key")
	}

	if len(branch) == 0 {
		return nil, errors.New("branch is required for creating a TNS key")
	}

	var value []string
	prefix := []string{string(common.BranchPathVariable), branch, string(common.ProjectPathVariable), projectId}
	if len(appId) == 0 {
		value = append(prefix, string(resourceType), resourceId)
	} else {
		value = append(prefix, string(common.ApplicationPathVariable), appId, string(resourceType), resourceId)
	}

	return common.NewTnsPath(value), nil
}

func GetBasicTNSKey(branch, commit, projectId, appId, resourceId string, resourceType common.PathVariable) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a TNS key")
	}

	if len(resourceId) == 0 {
		return nil, errors.New("resource id is required for creating a TNS key")
	}

	if len(commit) == 0 {
		return nil, errors.New("commit id is required for creating a TNS key")
	}

	if len(branch) == 0 {
		return nil, errors.New("branch is required for creating a TNS key")
	}

	var value []string
	prefix := []string{string(common.BranchPathVariable), branch, string(common.CommitPathVariable), commit, string(common.ProjectPathVariable), projectId}
	if len(appId) == 0 {
		value = append(prefix, string(resourceType), resourceId)
	} else {
		value = append(prefix, string(common.ApplicationPathVariable), appId, string(resourceType), resourceId)
	}

	return common.NewTnsPath(value), nil
}

func GetEmptyTNSKey(branch, commit, projectId, appId string, resourceType common.PathVariable) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("project id is required for creating a TNS key")
	}

	var value []string
	if len(appId) == 0 {
		value = []string{common.BranchPathVariable.String(), branch, common.CommitPathVariable.String(), commit, common.ProjectPathVariable.String(), projectId, resourceType.String()}
	} else {
		value = []string{common.BranchPathVariable.String(), branch, common.CommitPathVariable.String(), commit, common.ProjectPathVariable.String(), projectId, common.ApplicationPathVariable.String(), appId, resourceType.String()}
	}

	return common.NewTnsPath(value), nil
}
