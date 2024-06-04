package helpers

import (
	"math/big"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (m *methods) WriteUint32Le(module common.Module, ptr, toWrite uint32) errno.Error {
	written := module.Memory().WriteUint32Le(ptr, toWrite)
	if !written {
		return errno.ErrorMemoryWriteFailed
	}

	return 0
}

func (m *methods) WriteUint64Le(module common.Module, ptr uint32, toWrite uint64) errno.Error {
	written := module.Memory().WriteUint64Le(ptr, toWrite)
	if !written {
		return errno.ErrorMemoryWriteFailed
	}

	return 0
}

func (m *methods) WriteUint64LeInterface(module common.Module, ptr uint32, toWrite interface{}) errno.Error {
	value, ok := toWrite.(uint64)
	if !ok {
		value = 0
	}

	written := module.Memory().WriteUint64Le(ptr, value)
	if !written {
		return errno.ErrorMemoryWriteFailed
	}

	return 0
}

func (m *methods) ReadUint64Le(module common.Module, ptr uint32) (uint64, errno.Error) {
	value, ok := module.Memory().ReadUint64Le(ptr)
	if !ok {
		return 0, errno.ErrorAddressOutOfMemory
	}

	return value, 0
}

func (m *methods) ReadBigInt(module common.Module, ptr uint32, size uint32,
) (*big.Int, errno.Error) {
	buf, err := m.ReadBytes(module, ptr, size)
	if err != 0 {
		return nil, err
	}

	b := new(big.Int)
	return b.SetBytes(buf), 0
}
