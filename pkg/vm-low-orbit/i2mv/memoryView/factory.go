package memoryView

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, helper helpers.Methods) *Factory {
	return &Factory{parent: i, ctx: i.Context().Context(), Methods: helper, memoryViews: make(map[uint32]*MemoryView, 0)}
}

func (f *Factory) Name() string {
	return "i2mv"
}

func (f *Factory) Close() error {
	f.memoryViews = nil
	return nil
}

func (f *Factory) Load(hm vm.HostModule) (err error) {
	return nil
}
