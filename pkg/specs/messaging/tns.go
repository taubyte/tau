package messagingSpec

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
	"github.com/taubyte/tau/pkg/specs/methods"
	multihash "github.com/taubyte/tau/utils/multihash"
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
	if len(projectId) == 0 {
		return nil, errors.New("projectId required for websocket hash path")
	}

	return t.WebSocketPath(multihash.Hash(projectId + appId))
}

func (t *tnsHelper) WebSocketPath(hash string) (*common.TnsPath, error) {
	if len(hash) == 0 {
		return nil, errors.New("hash required for websocket hash path")
	}

	return common.NewTnsPath([]string{"p2p", "pubsub", hash}), nil
}
