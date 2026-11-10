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
// A database data step reads nothing again: see WriteReported.

// shortcut: sizes reported but never fetched are dropped all at once past this
// many, which only makes those later fetches behave as before the check.
const maxReportedSizes = 1024

// maxReportedBytes bounds the values a pooled instance holds between the size
// step and the data step, so a guest that asks and never fetches cannot pin
// memory. A larger value is simply read again in the data step.
const maxReportedBytes = 8 << 20

// WriteBytesSizeFor writes len(data) at ptr, as WriteBytesSize, and remembers
// it for the data step of key.
func (m *methods) WriteBytesSizeFor(module common.Module, key string, ptr uint32, data []byte) errno.Error {
	m.sizesLock.Lock()
	if m.sizes == nil || len(m.sizes) >= maxReportedSizes || m.valuesBytes+len(data) > maxReportedBytes {
		m.sizes, m.values, m.valuesBytes = make(map[string]uint32), make(map[string][]byte), 0
	}
	m.sizes[key] = uint32(len(data))
	m.valuesBytes -= len(m.values[key])
	delete(m.values, key)
	if len(data) > 0 && len(data) <= maxReportedBytes {
		m.values[key] = data
		m.valuesBytes += len(data)
	}
	m.sizesLock.Unlock()

	return m.WriteBytesSize(module, ptr, data)
}

// WriteReported writes the value the size step of key reported, so the data
// step costs no second read of it, and the two steps see one value that cannot
// change between them. ok is false when no value is held and the data step
// must read it itself.
func (m *methods) WriteReported(module common.Module, key string, ptr uint32) (rc errno.Error, ok bool) {
	m.sizesLock.Lock()
	value, ok := m.values[key]
	if ok {
		delete(m.sizes, key)
		delete(m.values, key)
		m.valuesBytes -= len(value)
	}
	m.sizesLock.Unlock()
	if !ok {
		return 0, false
	}

	return m.WriteBytes(module, ptr, value), true
}

// WriteBytesFor writes value at ptr, as WriteBytes, unless its size differs from
// the one last reported for key; then it returns ErrorSizeMismatch so the guest
// asks for the size again. With no size reported it behaves as WriteBytes.
func (m *methods) WriteBytesFor(module common.Module, key string, ptr uint32, value []byte) errno.Error {
	m.sizesLock.Lock()
	size, reported := m.sizes[key]
	delete(m.sizes, key)
	m.valuesBytes -= len(m.values[key])
	delete(m.values, key)
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
