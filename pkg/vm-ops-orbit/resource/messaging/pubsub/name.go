package messaging

import (
	"context"

	"github.com/taubyte/tau/core/vm"
)

func (f *MessagingPubSub) getMessagingPubSubName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) uint32 {
	message, err := f.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteString(module, dataPtr, message.Config().Name))
}

func (f *MessagingPubSub) getMessagingPubSubNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) uint32 {
	message, err := f.GetCaller(resourceId)
	if err != 0 {
		return uint32(err)
	}

	return uint32(f.WriteStringSize(module, sizePtr, message.Config().Name))
}
