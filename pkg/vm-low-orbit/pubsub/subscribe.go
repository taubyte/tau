package pubsub

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) setSubscriptionChannel(ctx context.Context, module common.Module,
	channelPtr, channelLen uint32,
) uint32 {
	channel, err := f.ReadString(module, channelPtr, channelLen)
	if err != 0 {
		return uint32(err)
	}

	_ctx := f.parent.Context()

	err0 := f.pubsubNode.Subscribe(_ctx.Project(), _ctx.Application(), _ctx.Resource(), channel)
	if err0 != nil {
		return uint32(errno.ErrorSubscribeFailed)
	}

	return uint32(0)
}
