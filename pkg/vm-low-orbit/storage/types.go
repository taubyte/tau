package storage

import (
	"context"
	"io"
	"sync"

	"github.com/ipfs/go-cid"
	storageIface "github.com/taubyte/tau/core/services/substrate/components/storage"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	storageNode      storageIface.Service
	parent           vm.Instance
	ctx              context.Context
	storagesLock     sync.RWMutex
	versionLock      sync.RWMutex
	storagesIdToGrab uint32
	storages         map[uint32]*Storage
	version          map[string]string
	contentLock      sync.RWMutex
	contents         map[uint32]*content
	contentIdToGrab  uint32
}

var _ vm.Factory = &Factory{}

type Storage struct {
	storageIface.Storage
	id           uint32
	fileLock     sync.RWMutex
	fileIdToGrab uint32
	files        map[uint32]*File
}

type content struct {
	id   uint32
	cid  cid.Cid
	file contentFile
}

type contentFile interface{}

type File struct {
	id      uint32
	version int
	cid     cid.Cid
	reader  io.ReadSeekCloser
}
