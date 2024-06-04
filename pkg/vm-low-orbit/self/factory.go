package self

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, helper helpers.Methods) *Factory {
	return &Factory{
		parent:  i,
		ctx:     i.Context().Context(),
		Methods: helper,
	}
}

func (f *Factory) Name() string {
	return "self"
}

func (f *Factory) Close() error {
	return nil
}

func (f *Factory) Load(hm vm.HostModule) (err error) {
	return nil
}
