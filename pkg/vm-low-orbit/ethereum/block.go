//go:build web3
// +build web3

package ethereum

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (c *Client) getBlock(blockId uint64) (*Block, errno.Error) {
	c.blocksLock.RLock()
	defer c.blocksLock.RUnlock()
	if block, ok := c.blocks[blockId]; ok {
		return block, errno.ErrorNone
	}

	return nil, errno.ErrorEthereumBlockNotFound
}

func (f *Factory) getBlock(clientId uint32, blockId uint64) (block *Block, err errno.Error) {
	client, err := f.getClient(clientId)
	if err != 0 {
		return
	}

	block, err = client.getBlock(blockId)

	return
}

func (f *Factory) ethBlockByNumber(
	ctx context.Context,
	module common.Module,
	clientId,
	size,
	bufPtr,
	blockIdPtr uint32,
) uint32 {
	c, err := f.getClient(clientId)
	if err != 0 {
		return uint32(err)
	}

	blockNumber, err := f.ReadBigInt(module, bufPtr, size)
	if err != 0 {
		return uint32(err)
	}

	block, err0 := c.BlockByNumber(f.ctx, blockNumber)
	if err0 != nil {
		return uint32(errno.ErrorEthereumBlockNotFound)
	}

	b := &Block{Id: block.NumberU64(), Block: block, transactions: make(map[uint32]*Transaction)}
	c.blocksLock.Lock()
	c.blocks[b.Id] = b
	c.blocksLock.Unlock()

	return uint32(f.WriteUint64Le(module, blockIdPtr, b.Id))
}

func (f *Factory) ethCurrentBlockNumber(
	ctx context.Context,
	module common.Module,
	clientId,
	blockNumberPtr uint32,
) uint32 {
	client, err0 := f.getClient(clientId)
	if err0 != 0 {
		return uint32(err0)
	}

	blockNumber, err := client.BlockNumber(f.ctx)
	if err != nil {
		return uint32(errno.ErrorEthereumBlockNotFound)
	}

	return uint32(f.WriteUint64Le(module, blockNumberPtr, blockNumber))
}

func (f *Factory) ethBlockNumberFromIdSize(
	ctx context.Context,
	module common.Module,
	clientId,
	blockIdPtr,
	lenPtr uint32,
) uint32 {
	blockId, err := f.ReadUint64Le(module, blockIdPtr)
	if err != 0 {
		return uint32(err)
	}

	block, err := f.getBlock(clientId, blockId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytesConvertibleSize(module, lenPtr, block.Number()))
}

func (f *Factory) ethBlockNumberFromId(
	ctx context.Context,
	module common.Module,
	clientId,
	blockIdPtr,
	bufPtr uint32,
) uint32 {
	blockId, err := f.ReadUint64Le(module, blockIdPtr)
	if err != 0 {
		return uint32(err)
	}

	block, err := f.getBlock(clientId, blockId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytesConvertible(module, bufPtr, block.Number()))
}
