package client

import (
	"context"
	"sync"

	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	databaseNode     dbIface.Service
	parent           vm.Instance
	CurrentKeystore  string
	ctx              context.Context
	databaseLock     sync.RWMutex
	databaseIdToGrab uint32
	database         map[uint32]*Database
}

var _ vm.Factory = &Factory{}

type Database struct {
	dbIface.Database
	Id uint32
}
