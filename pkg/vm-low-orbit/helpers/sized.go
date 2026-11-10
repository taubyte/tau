package helpers

import (
	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/go-sdk/utils/codec"
	common "github.com/taubyte/tau/core/vm"
)

// Some host calls are two steps: the guest asks for a value's size, allocates
// that many bytes, then asks for the data, which re-reads the value. The data
// step gets only a pointer, so the host remembers the size it reported under a
// key naming the value and refuses to write a value whose size changed since:
// one that grew would run past the guest's buffer into its other memory, and one
// that shrank would leave the guest reading zero bytes at the end of its buffer.

// shortcut: sizes reported but never fetched are dropped all at once past this
// many, which only makes those later fetches behave as before the check.
const maxReportedSizes = 1024

// WriteBytesSizeFor writes len(data) at ptr, as WriteBytesSize, and remembers
// it for the data step of key.
func (m *methods) WriteBytesSizeFor(module common.Module, key string, ptr uint32, data []byte) errno.Error {
	m.sizesLock.Lock()
	if m.sizes == nil || len(m.sizes) >= maxReportedSizes {
		m.sizes = make(map[string]uint32)
	}
	m.sizes[key] = uint32(len(data))
	m.sizesLock.Unlock()

	return m.WriteBytesSize(module, ptr, data)
}

// WriteBytesFor writes value at ptr, as WriteBytes, unless its size differs from
// the one last reported for key; then it returns ErrorSizeMismatch so the guest
// asks for the size again. With no size reported it behaves as WriteBytes.
func (m *methods) WriteBytesFor(module common.Module, key string, ptr uint32, value []byte) errno.Error {
	m.sizesLock.Lock()
	size, reported := m.sizes[key]
	delete(m.sizes, key)
	m.sizesLock.Unlock()

	if reported && uint32(len(value)) != size {
		return errno.ErrorSizeMismatch
	}

	return m.WriteBytes(module, ptr, value)
}

func (m *methods) WriteStringSliceSizeFor(module common.Module, key string, ptr uint32, value []string) errno.Error {
	var encoded []byte
	if err := codec.Convert(value).To(&encoded); err != nil {
		return errno.ErrorByteConversionFailed
	}

	return m.WriteBytesSizeFor(module, key, ptr, encoded)
}

func (m *methods) WriteStringSliceFor(module common.Module, key string, ptr uint32, value []string) errno.Error {
	var encoded []byte
	if err := codec.Convert(value).To(&encoded); err != nil {
		return errno.ErrorByteConversionFailed
	}

	return m.WriteBytesFor(module, key, ptr, encoded)
}
