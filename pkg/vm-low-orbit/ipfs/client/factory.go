//go:build web3
// +build web3

package client

import (
	"github.com/taubyte/tau/core/services/substrate/components/ipfs"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/helpers"
)

func New(i vm.Instance, ipfs ipfs.Service, helper helpers.Methods) *Factory {
	return &Factory{parent: i, ctx: i.Context().Context(), clients: make(map[uint32]*Client), ipfsNode: ipfs, Methods: helper}
}

func (f *Factory) Name() string {
	return "ipfs"
}

func (f *Factory) Close() error {
	f.clients = nil
	return nil
}
