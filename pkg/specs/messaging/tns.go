package messagingSpec

import (
	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
)

func Tns() *tnsHelper {
	return &tnsHelper{}
}

func (t *tnsHelper) EmptyPath(branch, commit, projectId, appId string) (*common.TnsPath, error) {
	return methods.GetEmptyTNSKey(branch, commit, projectId, appId, PathVariable)
}

func (t *tnsHelper) BasicPath(branch, commit, projectId, appId, msgId string) (*common.TnsPath, error) {
	return methods.GetBasicTNSKey(branch, commit, projectId, appId, msgId, PathVariable)
}

func (t *tnsHelper) IndexValue(branch, projectId, appId, msgId string) (*common.TnsPath, error) {
	return methods.IndexValue(branch, projectId, appId, msgId, PathVariable)
}

func (t *tnsHelper) WebSocketHashPath(projectId, appId string) (*common.TnsPath, error) {
	return methods.WebSocketHashPath(projectId, appId)
}

func (t *tnsHelper) WebSocketPath(hash string) (*common.TnsPath, error) {
	return methods.WebSocketPath(hash)
}
