package service

import (
	"github.com/taubyte/go-interfaces/kvdb"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/components/storage"
	"github.com/taubyte/odo/protocols/node/components/storage/common"
	"github.com/taubyte/odo/protocols/node/components/storage/storage"
)

func New(srv nodeIface.Service, options ...Option) (*Service, error) {
	s := &Service{
		Service:       srv,
		storages:      make(map[string]storageIface.Storage),
		matcher:       make(map[string]kvdb.KVDB, 0),
		commits:       make(map[string]string, 0),
		storageMethod: storage.New,
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			common.Logger.Errorf("Running option %v failed with %v", opt, err)
			return nil, err
		}
	}

	return s, nil
}
