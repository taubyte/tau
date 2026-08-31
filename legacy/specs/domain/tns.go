package domainSpec

import (
	"github.com/taubyte/tau/legacy/specs/common"
	"github.com/taubyte/tau/legacy/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}

func (t *tnsHelper) BasicPath(fqdn string) (*common.TnsPath, error) {
	return methods.ReversedFqdnBasicPath(fqdn, PathVariable)
}

func (t *tnsHelper) IndexValue(branch, projectId, appId, domId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, domId, PathVariable)
}
