package methods

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
	multihash "github.com/taubyte/tau/utils/multihash"
)

// WebSocketHashPath is the per-(project,app) websocket bucket path: it hashes
// project+app and defers to WebSocketPath. Generic across callers — the messaging
// spec delegates here.
func WebSocketHashPath(projectId, appId string) (*common.TnsPath, error) {
	if len(projectId) == 0 {
		return nil, errors.New("projectId required for websocket hash path")
	}

	return WebSocketPath(multihash.Hash(projectId + appId))
}

// WebSocketPath is the websocket bucket path p2p/pubsub/<hash>.
func WebSocketPath(hash string) (*common.TnsPath, error) {
	if len(hash) == 0 {
		return nil, errors.New("hash required for websocket hash path")
	}

	return common.NewTnsPath([]string{"p2p", "pubsub", hash}), nil
}
