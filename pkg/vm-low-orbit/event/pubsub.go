package event

import (
	"context"
	"strings"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) W_getMessageData(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	if e.pubsub == nil {
		return errno.ErrorNilAddress
	}

	return f.WriteBytes(module, bufPtr, e.pubsub.GetData())
}

func (f *Factory) W_getMessageDataSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	if e.pubsub == nil {
		return errno.ErrorNilAddress
	}

	return f.WriteBytesSize(module, sizePtr, e.pubsub.GetData())
}

func (f *Factory) W_getMessageChannel(ctx context.Context, module common.Module, eventId, channelPtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	if e.pubsub == nil {
		return errno.ErrorNilAddress
	}

	// hash/channelName
	splitTopic := strings.Split(e.pubsub.GetTopic(), "/")
	if len(splitTopic) != 2 {
		return errno.ErrorChannelNotFound
	}

	return f.WriteString(module, channelPtr, splitTopic[1])
}

func (f *Factory) W_getMessageChannelSize(ctx context.Context, module common.Module, eventId, sizePtr uint32) errno.Error {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return err
	}

	if e.pubsub == nil {
		return errno.ErrorNilAddress
	}

	// hash/channelName
	splitTopic := strings.Split(e.pubsub.GetTopic(), "/")
	if len(splitTopic) != 2 {
		return errno.ErrorChannelNotFound
	}

	return f.WriteStringSize(module, sizePtr, splitTopic[1])
}
