package pubsub

import (
	"context"

	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

type Factory struct {
	helpers.Methods
	pubsubNode pubsubIface.Service
	parent     vm.Instance
	ctx        context.Context
}

var _ vm.Factory = &Factory{}
