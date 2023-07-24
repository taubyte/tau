package common

import (
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	mh "github.com/taubyte/utils/multihash"
)

func GetDatabaseHash(c iface.Context) (string, error) {
	if len(c.ProjectId) < 1 {
		return "", fmt.Errorf("project ID is required")
	} else if len(c.Matcher) < 1 {
		return "", fmt.Errorf("database match is required")
	}

	return mh.Hash(c.ProjectId + c.ApplicationId + c.Matcher), nil
}
