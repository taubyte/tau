package dns

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, helper helpers.Methods) *Factory {
	return &Factory{
		parent:    i,
		ctx:       i.Context().Context(),
		resolvers: make(map[uint32]*Resolver),
		Methods:   helper,
	}
}

func (f *Factory) Name() string {
	return "dns"
}

func (f *Factory) Close() error {
	return nil
}

func (f *Factory) Load(hm vm.HostModule) (err error) {
	return nil
}
