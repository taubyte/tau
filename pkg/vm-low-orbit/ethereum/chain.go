//go:build web3
// +build web3

package ethereum

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) ethCurrentChainIdSize(
	ctx context.Context,
	module common.Module,
	clientId,
	sizePtr uint32,
) uint32 {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return uint32(err0)
	}

	chainId, err := client.ChainID(f.ctx)
	if err != nil {
		return uint32(errno.ErrorEthereumChainIdNotFound)
	}

	return uint32(f.WriteBytesConvertibleSize(module, sizePtr, chainId))
}

func (f *Factory) ethCurrentChainId(
	ctx context.Context,
	module common.Module,
	clientId,
	bufPtr uint32,
) uint32 {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return uint32(err0)
	}

	chainId, err := client.ChainID(f.ctx)
	if err != nil {
		return uint32(errno.ErrorEthereumChainIdNotFound)
	}

	return uint32(f.WriteBytesConvertible(module, bufPtr, chainId))
}
