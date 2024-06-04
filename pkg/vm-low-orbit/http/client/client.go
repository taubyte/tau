package client

import (
	"context"
	"net/http"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getClient(clientId uint32) (*Client, errno.Error) {
	f.clientsLock.RLock()
	defer f.clientsLock.RUnlock()
	if client, ok := f.clients[clientId]; ok {
		return client, 0
	}

	return nil, errno.ErrorClientNotFound
}

func (f *Factory) W_newHttpClient(ctx context.Context, module common.Module,
	clientIdPtr uint32,
) errno.Error {
	c := &Client{
		Id:     f.generateClientId(),
		Client: &http.Client{},
		reqs:   make(map[uint32]*Request),
	}

	f.clientsLock.Lock()
	defer f.clientsLock.Unlock()
	f.clients[c.Id] = c

	return f.WriteUint32Le(module, clientIdPtr, c.Id)
}
