package storage

import (
	"io"
	"os"

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
	for _, storage := range f.storages {
		storage.Close()
	}
	f.storagesLock.Unlock()

	// Content files never explicitly closed by the guest would otherwise leak
	// their fd and their on-disk temp file for the process lifetime.
	f.contentLock.Lock()
	for _, c := range f.contents {
		if closer, ok := c.file.(io.Closer); ok {
			closer.Close()
		}
		if c.path != "" {
			os.Remove(c.path)
		}
	}
	f.contents = nil
	f.contentLock.Unlock()

	return nil
}
