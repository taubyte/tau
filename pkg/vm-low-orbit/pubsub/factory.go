package pubsub

import (
	pubsubIface "github.com/taubyte/tau/core/services/substrate/components/pubsub"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, pubsubNode pubsubIface.Service, helper helpers.Methods) *Factory {
	return &Factory{parent: i, ctx: i.Context().Context(), pubsubNode: pubsubNode, Methods: helper}
}

func (f *Factory) Name() string {
	return "pubsub"
}

func (f *Factory) Close() error {
	return nil
}
