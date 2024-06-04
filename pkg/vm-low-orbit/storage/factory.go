package storage

import (
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, storageNode storageIface.Service, helper helpers.Methods) *Factory {
	return &Factory{
		parent:      i,
		ctx:         i.Context().Context(),
		storageNode: storageNode,
		storages:    make(map[uint32]*Storage),
		version:     make(map[string]string),
		contents:    make(map[uint32]*content),
		Methods:     helper,
	}
}

func (f *Factory) Name() string {
	return "storage"
}

func (f *Factory) Close() error {
	f.storagesLock.Lock()
	defer f.storagesLock.Unlock()
	for _, storage := range f.storages {
		storage.Close()
	}

	return nil
}

func (f *Factory) Load(hm vm.HostModule) (err error) {
	return nil
}
