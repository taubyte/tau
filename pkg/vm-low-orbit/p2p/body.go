package p2p

import (
	"context"

	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) readCommandResponse(ctx context.Context, module common.Module,
	commandId,
	dataBuf, dataSize uint32,
) (err uint32) {
	cmd, err0 := f.getCommand(commandId)
	if err0 != 0 {
		return uint32(err0)
	}

	return uint32(f.SafeWriteBytes(module, dataBuf, dataSize, cmd.Body))
}
