package event

import (
	"context"
	"strings"

	"github.com/taubyte/go-sdk/errno"
	common "github.com/taubyte/tau/core/vm"
)

func (f *Factory) getMessageData(ctx context.Context, module common.Module, eventId uint32, bufPtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	if e.pubsub == nil {
		return uint32(errno.ErrorNilAddress)
	}

	return uint32(f.WriteBytes(module, bufPtr, e.pubsub.GetData()))
}

func (f *Factory) getMessageDataSize(ctx context.Context, module common.Module, eventId uint32, sizePtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	if e.pubsub == nil {
		return uint32(errno.ErrorNilAddress)
	}

	return uint32(f.WriteBytesSize(module, sizePtr, e.pubsub.GetData()))
}

func (f *Factory) getMessageChannel(ctx context.Context, module common.Module, eventId, channelPtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	if e.pubsub == nil {
		return uint32(errno.ErrorNilAddress)
	}

	// hash/channelName
	splitTopic := strings.Split(e.pubsub.GetTopic(), "/")
	if len(splitTopic) != 2 {
		return uint32(errno.ErrorChannelNotFound)
	}

	return uint32(f.WriteString(module, channelPtr, splitTopic[1]))
}

func (f *Factory) getMessageChannelSize(ctx context.Context, module common.Module, eventId, sizePtr uint32) uint32 {
	e, err := f.getEvent(eventId)
	if err != 0 {
		return uint32(err)
	}

	if e.pubsub == nil {
		return uint32(errno.ErrorNilAddress)
	}

	// hash/channelName
	splitTopic := strings.Split(e.pubsub.GetTopic(), "/")
	if len(splitTopic) != 2 {
		return uint32(errno.ErrorChannelNotFound)
	}

	return uint32(f.WriteStringSize(module, sizePtr, splitTopic[1]))
}
