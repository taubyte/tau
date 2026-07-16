//go:build websocket

package main

//lint:file-ignore U1000 compiled file

import (
	"net/url"

	"github.com/taubyte/go-sdk/event"
	pubsub "github.com/taubyte/go-sdk/pubsub/node"
)

//export websockettest
func websockettest(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	url, err := func() (url url.URL, err error) {
		channel, err := pubsub.Channel("someChannel")
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

//export websockettestpublish
func websockettestpublish(e event.Event) uint32 {
	channel, err := pubsub.Channel("someChannel")
	if err != nil {
		return 1
	}

	err = channel.Publish([]byte("Hello from the other side"))
	if err != nil {
		h, err := e.HTTP()
		if err != nil {
			return 1
		}
		h.Write([]byte(err.Error()))
		return 1
	}

	return 0
}
