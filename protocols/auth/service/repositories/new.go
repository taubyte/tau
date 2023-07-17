package repositories

import (
	"errors"
	"fmt"

	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/utils/maps"
)

func New(kv kvdb.KVDB, data Data) (Repository, error) {
	provider, err := maps.String(data, "provider")
	if err != nil {
		return nil, err
	}

	// name, err := maps.String(data, "name")
	// if err != nil {
	// 	return nil, err
	// }

	project, _ := maps.String(data, "project")

	// url, err := maps.String(data, "url")
	// if err != nil {
	// 	return nil, err
	// }

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
				// Name:     name,
				Project: project,
				// Url:      url,
			},
			Id:  id,
			Key: key,
		}, nil
	default:
		return nil, errors.New(fmt.Sprintf("Unknows repo type `%s`", provider))
	}
}
