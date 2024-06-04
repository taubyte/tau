package fifo

import (
	"container/list"
	"context"
	"sync"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent    vm.Instance
	ctx       context.Context
	fifoMap   map[uint32]*Fifo
	fifoLock  sync.RWMutex
	idsToGrab uint32
}

type Fifo struct {
	id         uint32
	readCloser bool
	list       *list.List
}
