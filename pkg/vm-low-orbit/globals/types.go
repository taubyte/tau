package globals

import (
	"context"

	dbIface "github.com/taubyte/tau/core/services/substrate/components/database"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	parent       vm.Instance
	databaseNode dbIface.Service
	ctx          context.Context

	databaseInstance dbIface.Database
}

var _ vm.Factory = &Factory{}
