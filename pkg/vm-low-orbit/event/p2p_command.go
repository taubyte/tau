package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getP2PEventCommand(ctx context.Context, module common.Module, eventId, dataPtr uint32) errno.Error {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return err
	}

	_command, ok := data.cmd.Get("command")
	if ok {
		if err := data.cmd.SetName(_command); err != nil {
			return errno.ErrorP2PCommandNotFound
		}
	}

	return f.WriteString(module, dataPtr, data.cmd.Name())
}

func (f *Factory) W_getP2PEventCommandSize(ctx context.Context, module common.Module, eventId, sizePtr uint32) errno.Error {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return err
	}

	_command, ok := data.cmd.Get("command")
	if ok {
		if err := data.cmd.SetName(_command); err != nil {
			return errno.ErrorP2PCommandNotFound
		}
	}

	return f.WriteStringSize(module, sizePtr, data.cmd.Name())
}
