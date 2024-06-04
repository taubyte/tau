package helpers

import (
	"context"
	"math/big"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

type Methods interface {
	ReadBytes(module common.Module, ptr uint32, size uint32) ([]byte, errno.Error)
	WriteByte(module common.Module, ptr uint32, buf byte) errno.Error
	WriteBytesSize(module common.Module, ptr uint32, data []byte) errno.Error
	WriteBytes(module common.Module, ptr uint32, value []byte) errno.Error
	WriteBytesInterfaceSize(module common.Module, ptr uint32, iface interface{}) errno.Error
	WriteBytesInterface(module common.Module, ptr uint32, iface interface{}) errno.Error
	WriteBytesConvertibleSize(module common.Module, ptr uint32, value ByteConvertible) errno.Error
	WriteBytesConvertible(module common.Module, ptr uint32, value ByteConvertible) errno.Error
	WriteBytesConvertibleInterfaceSize(module common.Module, ptr uint32, iface interface{}) errno.Error
	WriteBytesConvertibleInterface(module common.Module, ptr uint32, iface interface{}) errno.Error
	WriteBytesConvertibleMultiSize(module common.Module, ptrs []uint32, values ...ByteConvertible) errno.Error
	WriteBytesConvertibleMulti(module common.Module, ptrs []uint32, values ...ByteConvertible) errno.Error
	WriteBytesSliceSize(module common.Module, ptr uint32, value [][]byte) errno.Error
	WriteBytesSlice(module common.Module, ptr uint32, value [][]byte) errno.Error
	ReadBytesSlice(module common.Module, ptr, size uint32) ([][]byte, errno.Error)
	ReadBigInt(module common.Module, ptr uint32, size uint32) (*big.Int, errno.Error)
	ReadString(module common.Module, ptr, len uint32) (string, errno.Error)
	WriteStringSize(module common.Module, ptr uint32, data string) errno.Error
	WriteString(module common.Module, ptr uint32, value string) errno.Error
	ReadStringSlice(module common.Module, ptr, len uint32) ([]string, errno.Error)
	WriteStringSliceSize(module common.Module, ptr uint32, value []string) errno.Error
	WriteStringSlice(module common.Module, ptr uint32, value []string) errno.Error
	WriteUint32SliceSize(module common.Module, ptr uint32, value []uint32) errno.Error
	WriteUint32Slice(module common.Module, ptr uint32, value []uint32) errno.Error
	ReadUint64Le(module common.Module, ptr uint32) (uint64, errno.Error)
	ReadUint64Slice(module common.Module, ptr, len uint32) ([]uint64, errno.Error)
	WriteUint32Le(module common.Module, ptr, toWrite uint32) errno.Error
	WriteUint64Le(module common.Module, ptr uint32, toWrite uint64) errno.Error
	WriteUint64LeInterface(module common.Module, ptr uint32, toWrite interface{}) errno.Error
	WriteUint64SliceSize(module common.Module, ptr uint32, value []uint64) errno.Error
	WriteUint64Slice(module common.Module, ptr uint32, toWrite []uint64) errno.Error
	WriteBool(module common.Module, ptr uint32, toWrite bool) errno.Error
	ReadBool(module common.Module, val uint32) (bool, errno.Error)
	SafeWriteBytes(module common.Module, ptr uint32, size uint32, value []byte) errno.Error
	Read(module common.Module,
		readMethod func(p []byte) (n int, err error),
		bufPtr, bufSize, // reader
		countPtr uint32, // reader size
	) errno.Error
	ReadCid(module common.Module, ptr uint32) (cid.Cid, errno.Error)
	WriteCid(module common.Module, ptr uint32, value cid.Cid) errno.Error
}

type methods struct {
	ctx context.Context
}

type ByteConvertible interface {
	Bytes() []byte
}
