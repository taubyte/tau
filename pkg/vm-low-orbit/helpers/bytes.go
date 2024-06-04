package helpers

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/codec"
	common "github.com/taubyte/tau/core/vm"
)

func (m *methods) ReadBytes(
	module common.Module,
	ptr uint32,
	size uint32,
) ([]byte, errno.Error) {
	buf, ok := module.Memory().Read(ptr, size)
	if !ok {
		return nil, errno.ErrorAddressOutOfMemory
	}

	return buf, 0
}

func (m *methods) WriteBytesSize(
	module common.Module,
	ptr uint32,
	data []byte,
) errno.Error {
	var size uint32
	if data != nil {
		size = uint32(len(data))
	}
	return m.WriteUint32Le(module, ptr, size)
}

func (m *methods) WriteBytes(
	module common.Module,
	ptr uint32,
	value []byte,
) errno.Error {
	if value == nil {
		value = make([]byte, 1)
	}

	ok := module.Memory().Write(ptr, value)
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	return 0
}

func (m *methods) WriteBytesInterfaceSize(
	module common.Module,
	ptr uint32,
	iface interface{},
) errno.Error {
	data, ok := iface.([]byte)
	if !ok {
		return errno.ErrorByteConversionFailed
	}

	return m.WriteUint32Le(module, ptr, uint32(len(data)))
}

func (m *methods) WriteBytesInterface(
	module common.Module,
	ptr uint32,
	iface interface{},
) errno.Error {
	data, ok := iface.([]byte)
	if !ok {
		return errno.ErrorByteConversionFailed
	}

	ok = module.Memory().Write(ptr, data)
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	return 0
}

func (m *methods) WriteBytesConvertibleMultiSize(module common.Module, ptrs []uint32, values ...ByteConvertible) errno.Error {
	if len(ptrs) != len(values) {
		return errno.ErrorAddressOutOfMemory
	}

	for idx, value := range values {
		err := m.WriteBytesConvertibleSize(module, ptrs[idx], value)
		if err != 0 {
			return err
		}
	}

	return 0
}

func (m *methods) WriteBytesConvertibleMulti(module common.Module, ptrs []uint32, values ...ByteConvertible) errno.Error {
	if len(ptrs) != len(values) {
		return errno.ErrorAddressOutOfMemory
	}

	for idx, value := range values {
		err := m.WriteBytesConvertible(module, ptrs[idx], value)
		if err != 0 {
			return err
		}
	}

	return 0
}

func BytesConvertibleMultiHelper(values ...ByteConvertible) (list []ByteConvertible) {
	list = append(list, values...)
	return
}

func (m *methods) WriteBytesConvertibleSize(
	module common.Module,
	ptr uint32,
	value ByteConvertible,
) errno.Error {
	return m.WriteUint32Le(module, ptr, uint32(len(checkNilConvertible(value))))
}

func (m *methods) WriteBytesConvertible(
	module common.Module,
	ptr uint32,
	value ByteConvertible,
) errno.Error {
	return m.WriteBytes(module, ptr, checkNilConvertible(value))
}

func (m *methods) WriteBytesConvertibleInterfaceSize(
	module common.Module,
	ptr uint32,
	iface interface{},
) errno.Error {
	value, ok := iface.(ByteConvertible)
	if !ok {
		return errno.ErrorConvertibleConversionFailed
	}

	return m.WriteBytesConvertibleSize(module, ptr, value)
}

func (m *methods) WriteBytesConvertibleInterface(
	module common.Module,
	ptr uint32,
	iface interface{},
) errno.Error {
	value, ok := iface.(ByteConvertible)
	if !ok {
		return errno.ErrorConvertibleConversionFailed
	}

	return m.WriteBytesConvertible(module, ptr, value)
}

func checkNilConvertible(value ByteConvertible) []byte {
	var bytes []byte
	if value != nil {
		bytes = value.Bytes()
	}

	return bytes
}

func (m *methods) WriteBytesSliceSize(module common.Module, ptr uint32, value [][]byte) errno.Error {
	var encoded []byte
	err0 := codec.Convert(value).To(&encoded)
	if err0 != nil {
		return errno.ErrorByteConversionFailed
	}

	return m.WriteBytesSize(module, ptr, encoded)
}

func (m *methods) WriteBytesSlice(module common.Module, ptr uint32, value [][]byte) errno.Error {
	var encoded []byte
	err0 := codec.Convert(value).To(&encoded)
	if err0 != nil {
		return errno.ErrorByteConversionFailed
	}

	return m.WriteBytes(module, ptr, encoded)
}

func (m *methods) ReadBytesSlice(module common.Module, ptr, size uint32) ([][]byte, errno.Error) {
	value, ok := module.Memory().Read(ptr, size)
	if !ok {
		return nil, errno.ErrorAddressOutOfMemory
	}

	var slice [][]byte
	err := codec.Convert(value).To(&slice)
	if err != nil {
		return nil, errno.ErrorByteConversionFailed
	}

	return slice, 0
}

func (m *methods) WriteByte(module common.Module, ptr uint32, buf byte) errno.Error {
	ok := module.Memory().WriteByte(ptr, buf)
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	return 0
}
