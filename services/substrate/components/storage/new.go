package substrate

import (
	"github.com/taubyte/tau/core/kvdb"
	nodeIface "github.com/taubyte/tau/core/services/substrate"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/services/substrate/components/storage/common"
	"github.com/taubyte/tau/services/substrate/components/storage/storage"
)

func New(srv nodeIface.Service, factory kvdb.Factory, options ...Option) (*Service, error) {
	s := &Service{
		Service:       srv,
		storages:      make(map[string]storageIface.Storage),
		matcher:       make(map[string]kvdb.KVDB, 0),
		commits:       make(map[string]string, 0),
		storageMethod: storage.New,
		dbFactory:     factory,
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			common.Logger.Errorf("Running option %v failed with %s", opt, err.Error())
			return nil, err
		}
	}

	return s, nil
}
