package librarySpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}

func (t *tnsHelper) BasicPath(branch, commit, projectId, appId, libId string) (*common.TnsPath, error) {
	return methods.GetBasicTNSKey(branch, commit, projectId, appId, libId, PathVariable)
}

func (t *tnsHelper) NameIndex(libId string) *common.TnsPath {
	return common.NewTnsPath([]string{PathVariable.String(), libId})
}

func (t *tnsHelper) IndexValue(branch, projectId, appId, libId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, libId, PathVariable)
}

func (t *tnsHelper) WasmModulePath(projectId, appId, resourceName string) (*common.TnsPath, error) {
	return methods.WasmModulePath(projectId, appId, resourceName, PathVariable)
}

func ModuleName(name string) string {
	return PathVariable.String() + "/" + name
}
