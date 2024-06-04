package ethereum

import (
	"context"
	"net/http"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/taubyte/go-sdk/errno"
	sdkRpc "github.com/taubyte/go-sdk/ethereum/client/rpc"
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

func (f *Factory) getClient(clientId uint32) (*Client, errno.Error) {
	f.clientsLock.RLock()
	defer f.clientsLock.RUnlock()
	if client, ok := f.clients[clientId]; ok {
		return client, 0
	}

	return nil, errno.ErrorClientNotFound
}

func (f *Factory) W_ethNew(ctx context.Context, module common.Module,
	clientIdPtr,
	urlPtr,
	urlLen,
	optionsPtr,
	optionsSize uint32,
) errno.Error {
	url, err0 := f.ReadString(module, urlPtr, urlLen)
	if err0 != 0 {
		return err0
	}

	var dialOptions []byte
	if optionsSize > 0 {
		dialOptions, err0 = f.ReadBytes(module, optionsPtr, optionsSize)
		if err0 != 0 {
			return err0
		}
	}

	rpcOpts := make([]rpc.ClientOption, 0)

	opts := sdkRpc.DialOptions{}
	if len(dialOptions) > 0 {
		if err := opts.UnmarshalJSON(dialOptions); err != nil {
			return errno.ErrorEthereumRPCOptionUnmarshalFailed
		}
	}

	if len(opts.Headers) > 0 {
		rpcOpts = append(rpcOpts, rpc.WithHeaders(http.Header(opts.Headers)))
	}

	rpcClient, err := rpc.DialOptions(f.ctx, url, rpcOpts...)
	if err != nil {
		return errno.ErrorEthereumNewClient
	}

	c := Client{
		Id:        f.generateClientId(),
		Client:    ethclient.NewClient(rpcClient),
		blocks:    make(map[uint64]*Block),
		contracts: make(map[uint32]*Contract),
	}

	f.clientsLock.Lock()
	defer f.clientsLock.Unlock()
	f.clients[c.Id] = &c

	return f.WriteUint32Le(module, clientIdPtr, c.Id)
}

func (f *Factory) W_ethCloseClient(
	ctx context.Context,
	module common.Module,
	clientId uint32,
) errno.Error {
	client, err := f.getClient(clientId)
	if err != 0 {
		return err
	}

	client.Close()
	delete(f.clients, clientId)

	return 0
}
