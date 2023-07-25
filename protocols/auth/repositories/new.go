package repositories

import (
	"fmt"

	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/utils/maps"
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

		return &GithubRepository{
			RepositoryCommon: RepositoryCommon{
				KV:       kv,
				Provider: provider,
				Project:  project,
			},
			Id:  id,
			Key: key,
		}, nil
	default:
		return nil, fmt.Errorf("unknown repo type `%s` ", provider)
	}
}
