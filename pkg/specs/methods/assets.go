package methods

import (
	"errors"

	"github.com/taubyte/tau/pkg/specs/common"
	multihash "github.com/taubyte/tau/utils/multihash"
)

func GetTNSAssetPath(projectId, resourceId, branch string) (*common.TnsPath, error) {
	if len(projectId) < 1 {
		return nil, errors.New("project Id is empty")
	}

	if len(resourceId) < 1 {
		return nil, errors.New("resource Id is empty")
	}

	if len(branch) < 1 {
		return nil, errors.New("branch Id is empty")
	}

	hashValue := multihash.Hash(projectId + resourceId + branch)
	return common.NewTnsPath([]string{"assets", hashValue}), nil
}
