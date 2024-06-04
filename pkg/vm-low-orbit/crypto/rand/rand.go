package rand

import (
	"context"
	"crypto/rand"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_cryptoRead(
	ctx context.Context,
	module vm.Module,
	bufPtr,
	bufLen,
	readPtr uint32,
) errno.Error {
	buf := make([]byte, bufLen)
	n, err := rand.Read(buf)
	if err != nil {
		return errno.ErrorRandRead
	}

	if err := f.WriteUint64Le(module, readPtr, uint64(n)); err != 0 {
		return err
	}

	return f.WriteBytes(module, bufPtr, buf)
}
