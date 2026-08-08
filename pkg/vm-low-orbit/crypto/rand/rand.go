package rand

import (
	"context"
	"crypto/rand"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (f *Factory) cryptoRead(
	ctx context.Context,
	module vm.Module,
	bufPtr,
	bufLen,
	readPtr uint32,
) uint32 {
	// Fill the guest's buffer in place. Memory().Read returns a slice aliasing
	// guest memory, so rand.Read writes straight into it: no host allocation
	// sized from the guest's bufLen (which rand.Read would otherwise commit page
	// by page) and no extra copy. Read bounds-checks bufPtr/bufLen for us.
	buf, ok := module.Memory().Read(bufPtr, bufLen)
	if !ok {
		return uint32(errno.ErrorAddressOutOfMemory)
	}

	n, err := rand.Read(buf)
	if err != nil {
		return uint32(errno.ErrorRandRead)
	}

	return uint32(f.WriteUint64Le(module, readPtr, uint64(n)))
}
