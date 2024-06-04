package messaging

import (
	"context"

	"github.com/taubyte/go-sdk/errno"
	"github.com/taubyte/tau/core/vm"
)

func (f *MessagingPubSub) W_getMessagingPubSubName(ctx context.Context, module vm.Module, resourceId uint32, dataPtr uint32) errno.Error {
	message, err := f.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteString(module, dataPtr, message.Config().Name)
}

func (f *MessagingPubSub) W_getMessagingPubSubNameSize(ctx context.Context, module vm.Module, resourceId uint32, sizePtr uint32) errno.Error {
	message, err := f.GetCaller(resourceId)
	if err != 0 {
		return err
	}

	return f.WriteStringSize(module, sizePtr, message.Config().Name)
}
