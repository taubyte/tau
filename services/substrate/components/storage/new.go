package substrate

import (
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/services/substrate/components/storage/common"
	"github.com/taubyte/tau/services/substrate/components/storage/storage"
)

func New(srv nodeIface.Service, hoarderClient hoarderIface.Client, options ...Option) (*Service, error) {
	s := &Service{
		Service:       srv,
		hoarderClient: hoarderClient,
		storages:      make(map[string]storageIface.Storage),
		commits:       make(map[string]string, 0),
		storageMethod: storage.New,
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			common.Logger.Errorf("Running option %v failed with %s", opt, err.Error())
			return nil, err
		}
	}

	return s, nil
}
