//go:build websocket_method

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	httpEvent "github.com/taubyte/go-sdk/http/event"
	pubsub "github.com/taubyte/go-sdk/pubsub/node"
)

//export methodGetUrl
func methodGetUrl(e event.Event) uint32 {
	h, err := e.HTTP()
	if err == nil {
		if err := runTestGetURL(h); err != nil {
			h.Write([]byte(fmt.Sprintf("Running runTestGetURL failed with: %s", err)))
			return 1
		}
	}

	return 0
}

func runTestGetURL(h httpEvent.Event) error {
	ch, err := pubsub.Channel("someChannel")
	if err != nil {
		return err
	}

	url, err := ch.WebSocket().Url()
	if err != nil {
		return err
	}

	_, err = h.Write([]byte(url.Path))
	return err
}

//export methodWebSocketPublish
func methodWebSocketPublish(e event.Event) uint32 {
	h, err := e.HTTP()
	if err == nil {
		if err := runTestPublishWebSocket(h); err != nil {
			h.Write([]byte(fmt.Sprintf("Running runTestPublishWebSocket failed with: %s", err)))
			return 1
		}
	}

	return 0
}

func runTestPublishWebSocket(h httpEvent.Event) error {
	ch, err := pubsub.Channel("someChannel")
	if err != nil {
		return err
	}

	err = ch.Subscribe()
	if err != nil {
		return err
	}

	err = ch.Publish([]byte("Hello, world!"))
	if err != nil {
		return err
	}

	_, err = h.Write([]byte("Success"))
	return err
}
