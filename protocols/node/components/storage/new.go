package service

import (
	"fmt"

	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/go-interfaces/moody"
	nodeIface "github.com/taubyte/go-interfaces/services/substrate"
	storageIface "github.com/taubyte/go-interfaces/services/substrate/storage"
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
			s.Logger().Error(moody.Object{"message": fmt.Sprintf("Running option %v failed with %v", opt, err), "service": "storage.service"})
			return nil, err
		}
	}

	return s, nil
}
