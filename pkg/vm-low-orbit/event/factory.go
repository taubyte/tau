package event

import (
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, helper helpers.Methods) *Factory {
	return &Factory{parent: i, ctx: i.Context().Context(), events: make(map[uint32]*Event), Methods: helper}
}

func (f *Factory) Name() string {
	return "event"
}

func (f *Factory) Close() error {
	f.events = nil
	return nil
}
