package event

import (
	"context"
	"encoding/json"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) hostGetP2PEventData(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	if data == nil {
		return uint32(errno.ErrorNilAddress)
	}

	return uint32(f.WriteBytes(module, bufPtr, data.marshalledData))
}

func (f *Factory) hostGetP2PEventDataSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) uint32 {
	data, err := f.getP2PEventData(eventId)
	if err != 0 {
		return uint32(err)
	}

	_data, ok := data.cmd.Get("data")
	if !ok {
		var err0 error
		data.marshalledData, err0 = json.Marshal(data.cmd.Raw())
		if err0 != nil {
			return uint32(errno.ErrorMarshalDataFailed)
		}

		return uint32(f.WriteBytesSize(module, sizePtr, data.marshalledData))
	}

	data.marshalledData, ok = _data.([]byte)
	if !ok {
		return uint32(errno.ErrorMarshalDataFailed)
	}

	return uint32(f.WriteBytesSize(module, sizePtr, data.marshalledData))
}
