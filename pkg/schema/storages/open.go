package storages

import (
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/pkg/schema/basic"
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
