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
	buf := make([]byte, bufLen)
	n, err := rand.Read(buf)
	if err != nil {
		return uint32(errno.ErrorRandRead)
	}

	if err := f.WriteUint64Le(module, readPtr, uint64(n)); err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteBytes(module, bufPtr, buf))
}
