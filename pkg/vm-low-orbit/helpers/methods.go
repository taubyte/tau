package helpers

import (
	"io"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

// Writes to a buffer pointer of size
func (m *methods) Read(module common.Module,
	readMethod func(p []byte) (n int, err error),
	bufPtr, bufSize, // reader
	countPtr uint32, // reader size
) errno.Error {
	// Read straight into the guest's linear memory. wazy's Memory().Read returns
	// a slice aliasing that memory, so readMethod fills it in place: no host
	// allocation sized from the guest's bufSize and no extra buf->guest copy.
	// Read also bounds-checks bufPtr/bufSize, so an oversized guest length is
	// rejected here instead of driving a multi-GiB allocation.
	buf, ok := module.Memory().Read(bufPtr, bufSize)
	if !ok {
		return errno.ErrorAddressOutOfMemory
	}

	n, err0 := readMethod(buf)
	if err0 != nil && err0 != io.EOF {
		return errno.ErrorHttpReadBody
	}

	if !module.Memory().WriteUint32Le(countPtr, uint32(n)) {
		return errno.ErrorAddressOutOfMemory
	}

	if err0 == io.EOF {
		return errno.ErrorEOF
	}

	return 0
}
