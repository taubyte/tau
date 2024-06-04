package websiteSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}

func (t *tnsHelper) BasicPath(branch, commit, projectId, appId, webId string) (*common.TnsPath, error) {
	return methods.GetBasicTNSKey(branch, commit, projectId, appId, webId, PathVariable)
}

func (t *tnsHelper) IndexValue(branch, projectId, appId, webId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, webId, PathVariable)
}

func (t *tnsHelper) HttpPath(fqdn string) (*common.TnsPath, error) {
	return methods.HttpPath(fqdn, PathVariable)
}

func (t *tnsHelper) WasmModulePath(projectId, appId, resourceName string) (*common.TnsPath, error) {
	return methods.WasmModulePath(projectId, appId, resourceName, PathVariable)
}
