package lib

import (
	"github.com/taubyte/go-sdk/event"
	pubsubNode "github.com/taubyte/go-sdk/pubsub/node"
)

const replyChannel = "replychannel"

//lint:ignore U1000 wasm export
//export pubsubEcho
func pubsubEcho(e event.Event) uint32 {
	pubSubEvent, err := e.PubSub()
	if err != nil {
		return 1
	}

	data, err := pubSubEvent.Data()
	if err != nil {
		return 1
	}

	// exercise the channel accessor too, matching the incoming trigger channel
	if _, err := pubSubEvent.Channel(); err != nil {
		return 1
	}

	reply, err := pubsubNode.Channel(replyChannel)
	if err != nil {
		return 1
	}

	if err := reply.Publish(append([]byte("echo:"), data...)); err != nil {
		return 1
	}

	return 0
}
