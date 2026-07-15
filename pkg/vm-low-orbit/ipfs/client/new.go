//go:build web3
// +build web3

package client

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) generateClientId() uint32 {
	f.clientsLock.Lock()
	defer func() {
		f.clientsIdToGrab += 1
		f.clientsLock.Unlock()
	}()
	return f.clientsIdToGrab
}

func (f *Factory) newIpfsClient(ctx context.Context, module common.Module,
	clientIdPtr uint32,
) uint32 {
	c := &Client{
		Id:       f.generateClientId(),
		Contents: make(map[uint32]*content),
	}

	f.clientsLock.Lock()
	defer f.clientsLock.Unlock()
	f.clients[c.Id] = c

	return uint32(f.WriteUint32Le(module, clientIdPtr, c.Id))
}
