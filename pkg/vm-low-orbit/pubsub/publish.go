package pubsub

import (
	"context"
	"io"

	"github.com/ipfs/go-log/v2"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm-low-orbit/memory"
)

// The guest only gets an error code: without this the cause is lost.
var logger = log.Logger("tau.substrate.pubsub")

func (f *Factory) publishToChannel(ctx context.Context, module common.Module,
	channelPtr, channelLen,
	bodyPtr, bodySize uint32,
) uint32 {
	channel, err := f.ReadString(module, channelPtr, channelLen)
	if err != 0 {
		return uint32(err)
	}

	_ctx := f.parent.Context()

	readCloser := memory.New(f.ctx, module.Memory(), bodyPtr, bodySize)
	defer readCloser.Close()
	data, err0 := io.ReadAll(readCloser)
	if err0 != nil {
		return uint32(errno.ErrorEOF)
	}

	err0 = f.pubsubNode.Publish(ctx, _ctx.Project(), _ctx.Application(), _ctx.Resource(), channel, data)
	if err0 != nil {
		logger.Errorf("publishing to channel %q of project %s failed: %v", channel, _ctx.Project(), err0)
		return uint32(errno.ErrorPublishFailed)
	}

	return uint32(0)
}
