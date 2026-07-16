//go:build websocket_room

package main

//lint:file-ignore U1000 compiled file

import (
	"net/url"

	"github.com/taubyte/go-sdk/event"
	httpEvent "github.com/taubyte/go-sdk/http/event"
	pubsub "github.com/taubyte/go-sdk/pubsub/node"
	// "github.com/taubyte/go-sdk/pubsub"
)

func getChannel(h httpEvent.Event) string {
	room, _ := h.Query().Get("room")

	channelName := "someChannel"
	if len(room) > 0 {
		channelName += "/" + room
	}

	return channelName
}

//export getsocketurlroom
func getsocketurlroom(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	url, err := func() (url url.URL, err error) {
		channel, err := pubsub.Channel(getChannel(h))
		if err != nil {
			return
		}

		return channel.WebSocket().Url()
	}()
	if err != nil {
		h, err := e.HTTP()
		if err != nil {
			return 1
		}

		h.Write([]byte(err.Error()))
		return 1
	}

	h.Write([]byte(url.Path))

	return 0
}

//export websockettestpublishroom
func websockettestpublishroom(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	channel, err := pubsub.Channel(getChannel(h))
	if err != nil {
		return 1
	}

	err = channel.Publish([]byte("Hello from the other side"))
	if err != nil {
		h.Write([]byte(err.Error()))
		return 1
	}

	return 0
}
