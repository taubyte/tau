package common

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
	mh "github.com/taubyte/utils/multihash"
)

func GetStorageHash(c iface.Context) (string, error) {
	if len(c.ProjectId) == 0 {
		return "", fmt.Errorf("project ID is required")
	}
	if len(c.Matcher) == 0 {
		return "", fmt.Errorf("storage match is required")
	}

	return mh.Hash(c.ProjectId + c.ApplicationId + c.Matcher), nil
}
