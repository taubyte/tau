package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getP2PEventProtocol(ctx context.Context, module common.Module, eventId, dataPtr uint32) errno.Error {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return err
	}

	if len(data.protocol) == 0 {
		return errno.ErrorP2PProtocolNotFound
	}

	return f.WriteString(module, dataPtr, data.protocol)
}

func (f *Factory) W_getP2PEventProtocolSize(ctx context.Context, module common.Module, eventId, sizePtr uint32) errno.Error {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return err
	}

	_protocol, ok := data.cmd.Get("protocol")
	if !ok {
		return errno.ErrorP2PProtocolNotFound
	}
	data.protocol, ok = _protocol.(string)
	if !ok {
		return errno.ErrorP2PProtocolNotFound
	}

	return f.WriteStringSize(module, sizePtr, data.protocol)
}
