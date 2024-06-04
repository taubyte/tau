package memoryView

import (
	"context"
	"sync"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent      vm.Instance
	ctx         context.Context
	memoryViews map[uint32]*MemoryView
	mvLock      sync.RWMutex
	idsToGrab   uint32
}

type MemoryView struct {
	id       uint32
	size     uint32
	bufPtr   uint32
	closable bool
	module   vm.Module
}
