package databaseSpec

import (
	"github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/legacy/specs/methods"
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
	return methods.IndexPath(projectId, appId, name)
}
