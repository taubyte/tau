package functionSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}

func (t *tnsHelper) BasicPath(branch, commit, projectId, appId, funcId string) (*common.TnsPath, error) {
	return methods.GetBasicTNSKey(branch, commit, projectId, appId, funcId, PathVariable)
}

func (t *tnsHelper) IndexValue(branch, projectId, appId, funcId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, funcId, PathVariable)
}

func (t *tnsHelper) HttpPath(fqdn string) (*common.TnsPath, error) {
	return methods.HttpPath(fqdn, PathVariable)
}

func (t *tnsHelper) WasmModulePath(projectId, appId, resourceName string) (*common.TnsPath, error) {
	return methods.WasmModulePath(projectId, appId, resourceName, PathVariable)
}

func ModuleName(name string) string {
	return PathVariable.String() + "/" + name
}

func (t *tnsHelper) ServicesPath(projectId, appId, serviceId, command string) (*common.TnsPath, error) {
	return methods.ServicePath(projectId, appId, serviceId, command)
}
