package p2p

import (
	p2pIface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, p2pNode p2pIface.Service, helper helpers.Methods) *Factory {
	return &Factory{
		parent:   i,
		ctx:      i.Context().Context(),
		commands: make(map[uint32]*Command),
		streams:  make(map[string]p2pIface.Stream),
		discover: make(map[uint32][][]byte),
		p2pNode:  p2pNode,
		Methods:  helper,
	}
}

func (f *Factory) Name() string {
	return "p2p"
}

func (f *Factory) Close() error {
	f.commands = nil
	f.discover = nil
	for _, stream := range f.streams {
		stream.Close()
	}
	return nil
}
