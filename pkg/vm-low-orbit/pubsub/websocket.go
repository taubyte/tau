package pubsub

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getSocketURL(module common.Module, channelPtr, channelLen uint32) (url string, err errno.Error) {
	channel, err := f.ReadString(module, channelPtr, channelLen)
	if err != 0 {
		return "", err
	}

	_ctx := f.parent.Context()
	socketURL, err0 := f.pubsubNode.WebSocketURL(_ctx.Project(), _ctx.Application(), channel)
	if err0 != nil {
		return "", errno.ErrorGetWebSocketURLFailed
	}

	return socketURL, 0
}

func (f *Factory) W_getWebSocketURLSize(ctx context.Context, module common.Module,
	channelPtr, channelLen,
	sizePtr uint32,
) errno.Error {

	socketURL, err := f.getSocketURL(module, channelPtr, channelLen)
	if err != 0 {
		return err
	}

	return f.WriteStringSize(module, sizePtr, socketURL)
}

func (f *Factory) W_getWebSocketURL(ctx context.Context, module common.Module,
	channelPtr, channelLen,
	socketURLPtr uint32,
) errno.Error {

	socketURL, err := f.getSocketURL(module, channelPtr, channelLen)
	if err != 0 {
		return err
	}

	return f.WriteString(module, socketURLPtr, socketURL)
}
