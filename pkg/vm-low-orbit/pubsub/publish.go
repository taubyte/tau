package pubsub

import (
	"context"
	"io"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/memory"
)

func (f *Factory) W_publishToChannel(ctx context.Context, module common.Module,
	channelPtr, channelLen,
	bodyPtr, bodySize uint32,
) (err errno.Error) {
	channel, err := f.ReadString(module, channelPtr, channelLen)
	if err != 0 {
		return
	}

	_ctx := f.parent.Context()

	readCloser := memory.New(f.ctx, module.Memory(), bodyPtr, bodySize)
	defer readCloser.Close()
	data, err0 := io.ReadAll(readCloser)
	if err0 != nil {
		return errno.ErrorEOF
	}

	err0 = f.pubsubNode.Publish(ctx, _ctx.Project(), _ctx.Application(), _ctx.Resource(), channel, data)
	if err0 != nil {
		return errno.ErrorPublishFailed
	}

	return 0
}
