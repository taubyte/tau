//go:build web3
// +build web3

package ethereum

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_ethCurrentChainIdSize(
	ctx context.Context,
	module common.Module,
	clientId,
	sizePtr uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	chainId, err := client.ChainID(f.ctx)
	if err != nil {
		return errno.ErrorEthereumChainIdNotFound
	}

	return f.WriteBytesConvertibleSize(module, sizePtr, chainId)
}

func (f *Factory) W_ethCurrentChainId(
	ctx context.Context,
	module common.Module,
	clientId,
	bufPtr uint32,
) errno.Error {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return err0
	}

	chainId, err := client.ChainID(f.ctx)
	if err != nil {
		return errno.ErrorEthereumChainIdNotFound
	}

	return f.WriteBytesConvertible(module, bufPtr, chainId)
}
