package client

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, helper helpers.Methods) *Factory {
	return &Factory{parent: i, ctx: i.Context().Context(), Methods: helper}
}

func (f *Factory) Name() string {
	return "client"
}

func (f *Factory) Close() error {
	f.clients = nil
	return nil
}

func (f *Factory) Load(hm vm.HostModule) error {
	f.clients = map[uint32]*Client{}
	return nil
}
