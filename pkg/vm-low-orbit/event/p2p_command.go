package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getP2PEventCommand(ctx context.Context, module common.Module, eventId, dataPtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	_command, ok := data.cmd.Get("command")
	if ok {
		if err := data.cmd.SetName(_command); err != nil {
			return uint32(errno.ErrorP2PCommandNotFound)
		}
	}

	return uint32(f.WriteString(module, dataPtr, data.cmd.Name()))
}

func (f *Factory) getP2PEventCommandSize(ctx context.Context, module common.Module, eventId, sizePtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	_command, ok := data.cmd.Get("command")
	if ok {
		if err := data.cmd.SetName(_command); err != nil {
			return uint32(errno.ErrorP2PCommandNotFound)
		}
	}

	return uint32(f.WriteStringSize(module, sizePtr, data.cmd.Name()))
}
