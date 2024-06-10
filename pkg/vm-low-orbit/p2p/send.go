package p2p

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
	res "github.com/taubyte/tau/p2p/streams/command/response"
)

func (f *Factory) W_sendCommand(ctx context.Context, module common.Module,
	commandId,
	dataBuf, dataSize, // body
	responseSize uint32,
) (err0 errno.Error) {
	cmd, err0 := f.getCommand(commandId)
	if err0 != 0 {
		return
	}

	data, err0 := f.ReadBytes(module, dataBuf, dataSize)
	if err0 != 0 {
		return
	}

	responseMap, err := cmd.Send(ctx, map[string]interface{}{"data": data})
	if err != nil {
		return errno.ErrorP2PSendFailed
	}

	return f.handleCommandResponse(module, responseMap, cmd, responseSize)
}

func (f *Factory) W_sendCommandTo(ctx context.Context, module common.Module,
	commandId,
	cidBuf,
	dataBuf, dataSize,
	responseSize uint32,
) (err0 errno.Error) {
	cmd, err0 := f.getCommand(commandId)
	if err0 != 0 {
		return
	}

	cid, err0 := f.ReadCid(module, cidBuf)
	if err0 != 0 {
		return
	}

	data, err0 := f.ReadBytes(module, dataBuf, dataSize)
	if err0 != 0 {
		return
	}

	responseMap, err := cmd.SendTo(ctx, cid, map[string]interface{}{"data": data})
	if err != nil {
		return errno.ErrorP2PSendFailed
	}

	return f.handleCommandResponse(module, responseMap, cmd, responseSize)
}

func (f *Factory) handleCommandResponse(module common.Module, response res.Response, cmd *Command, responseSize uint32) errno.Error {
	data, err := response.Get("data")
	if err != nil {
		return errno.ErrorMarshalDataFailed
	}

	var ok bool
	cmd.Body, ok = data.([]byte)
	if !ok {
		return errno.ErrorMarshalDataFailed
	}

	return f.WriteBytesSize(module, responseSize, cmd.Body)
}
