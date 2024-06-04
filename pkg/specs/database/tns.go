package databaseSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}

}

func (t *tnsHelper) BasicPath(branch, commit, projectId, appId, dbId string) (*common.TnsPath, error) {
	return methods.GetBasicTNSKey(branch, commit, projectId, appId, dbId, PathVariable)
}

func (t *tnsHelper) IndexValue(branch, projectId, appId, dbId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, dbId, PathVariable)
}

func (t *tnsHelper) IndexPath(projectId, appId, name string) *common.TnsPath {
	if len(appId) > 0 {
		return common.NewTnsPath([]string{common.ProjectPathVariable.String(), projectId, common.ApplicationPathVariable.String(), appId, name})
	}

	return common.NewTnsPath([]string{common.ProjectPathVariable.String(), projectId, name})
}
