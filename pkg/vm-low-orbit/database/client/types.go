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
	// byMatch is the open handle of each database this instance uses. An
	// instance is pooled across calls, and a function opening its database on
	// every call without closing it added a handle per call.
	byMatch map[string]*Database
}

var _ vm.Factory = &Factory{}

type Database struct {
	dbIface.Database
	Id    uint32
	match string
	refs  int // opens not yet closed; the handle goes at the last close
}
