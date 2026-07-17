package serviceSpec

import (
	"github.com/taubyte/tau/pkg/tcc/internal/parity/specs/common"
	"github.com/taubyte/tau/pkg/tcc/internal/parity/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}
func (t *tnsHelper) IndexValue(branch, projectId, appId, serviceId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, serviceId, PathVariable)
}

func (t *tnsHelper) EmptyPath(branch, commit, projectId, appId string) (*common.TnsPath, error) {
	return methods.GetEmptyTNSKey(branch, commit, projectId, appId, PathVariable)
}
