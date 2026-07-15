package event

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getP2PEventProtocol(ctx context.Context, module common.Module, eventId, dataPtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	if len(data.protocol) == 0 {
		return uint32(errno.ErrorP2PProtocolNotFound)
	}

	return uint32(f.WriteString(module, dataPtr, data.protocol))
}

func (f *Factory) getP2PEventProtocolSize(ctx context.Context, module common.Module, eventId, sizePtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	_protocol, ok := data.cmd.Get("protocol")
	if !ok {
		return uint32(errno.ErrorP2PProtocolNotFound)
	}
	data.protocol, ok = _protocol.(string)
	if !ok {
		return uint32(errno.ErrorP2PProtocolNotFound)
	}

	return uint32(f.WriteStringSize(module, sizePtr, data.protocol))
}
