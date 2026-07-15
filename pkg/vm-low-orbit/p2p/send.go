package p2p

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
	res "github.com/taubyte/tau/p2p/streams/command/response"
)

func (f *Factory) sendCommand(ctx context.Context, module common.Module,
	commandId,
	dataBuf, dataSize, // body
	responseSize uint32,
) (err0 uint32) {
	cmd, err := f.getCommand(commandId)
	if err != 0 {
		return uint32(err)
	}

	data, err := f.ReadBytes(module, dataBuf, dataSize)
	if err != 0 {
		return uint32(err)
	}

	responseMap, err2 := cmd.Send(ctx, map[string]interface{}{"data": data})
	if err2 != nil {
		return uint32(errno.ErrorP2PSendFailed)
	}

	return uint32(f.handleCommandResponse(module, responseMap, cmd, responseSize))
}

func (f *Factory) sendCommandTo(ctx context.Context, module common.Module,
	commandId,
	cidBuf,
	dataBuf, dataSize,
	responseSize uint32,
) (err0 uint32) {
	cmd, err := f.getCommand(commandId)
	if err != 0 {
		return uint32(err)
	}

	cid, err := f.ReadCid(module, cidBuf)
	if err != 0 {
		return uint32(err)
	}

	data, err := f.ReadBytes(module, dataBuf, dataSize)
	if err != 0 {
		return uint32(err)
	}

	responseMap, err2 := cmd.SendTo(ctx, cid, map[string]interface{}{"data": data})
	if err2 != nil {
		return uint32(errno.ErrorP2PSendFailed)
	}

	return uint32(f.handleCommandResponse(module, responseMap, cmd, responseSize))
}

func (f *Factory) handleCommandResponse(module common.Module, response res.Response, cmd *Command, responseSize uint32) uint32 {
	data, err := response.Get("data")
	if err != nil {
		return uint32(errno.ErrorMarshalDataFailed)
	}

	var ok bool
	cmd.Body, ok = data.([]byte)
	if !ok {
		return uint32(errno.ErrorMarshalDataFailed)
	}

	return uint32(f.WriteBytesSize(module, responseSize, cmd.Body))
}
