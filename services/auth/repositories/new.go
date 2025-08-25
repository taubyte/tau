package repositories

import (
	"fmt"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/utils/maps"
)

// TODO: Verbose errors
func New(kv kvdb.KVDB, data Data) (Repository, error) {
	provider, err := maps.String(data, "provider")
	if err != nil {
		return nil, err
	}

	project, _ := maps.String(data, "project")
	switch provider {
	case "github":
		id, err := maps.Int(data, "id")
		if err != nil {
			return nil, err
		}

		key, err := maps.String(data, "key")
		if err != nil {
			return nil, err
		}

		return &githubRepository{
			repositoryCommon: repositoryCommon{
				kv:       kv,
				provider: provider,
				project:  project,
			},
			id:  id,
			key: key,
		}, nil
	default:
		return nil, fmt.Errorf("unknown repo type `%s` ", provider)
	}
}
