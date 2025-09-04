package storages

import (
	"github.com/taubyte/tau/pkg/schema/basic"
	seer "github.com/taubyte/tau/pkg/yaseer"
)

func Open(seer *seer.Seer, name string, application string) (Storage, error) {
	storage := &storage{
		seer:        seer,
		name:        name,
		application: application,
	}

	var err error
	storage.Resource, err = basic.New(seer, storage, name)
	if err != nil {
		return nil, err
	}

	return storage, nil
}
