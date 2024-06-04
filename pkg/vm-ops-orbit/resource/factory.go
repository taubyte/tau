package resource

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

var _ common.Factory = &Factory{}

func New(i vm.Instance, helper helpers.Methods) *Factory {
	f := &Factory{
		parent:  i,
		ctx:     i.Context().Context(),
		Methods: helper,
	}

	return f
}

func (f *Factory) Name() string {
	return "resource"
}

func (f *Factory) Close() error {
	f.resources = nil
	return nil
}
