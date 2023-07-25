package common

import (
	"errors"

	iface "github.com/taubyte/go-interfaces/services/substrate/components/database"
	mh "github.com/taubyte/utils/multihash"
)

func GetDatabaseHash(c iface.Context) (string, error) {
	if len(c.ProjectId) < 1 || len(c.Matcher) < 1 {
		return "", errors.New("project ID and matcher required")
	}

	return mh.Hash(c.ProjectId + c.ApplicationId + c.Matcher), nil
}
