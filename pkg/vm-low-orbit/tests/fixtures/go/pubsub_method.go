//go:build pubsub_method

package main

//lint:file-ignore U1000 compiled file

import (
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/common"
	"github.com/taubyte/go-sdk/event"
	httpEvent "github.com/taubyte/go-sdk/http/event"
	pubsubEvent "github.com/taubyte/go-sdk/pubsub/event"
	pubsub "github.com/taubyte/go-sdk/pubsub/node"
)

//export methodPubSub
func methodPubSub(e event.Event) uint32 {
	if e.Type() == common.EventTypePubsub {
		p, err := e.PubSub()
		if err != nil {
			fmt.Println("ERR", err)
			return 1
		}

		if err := runTestMethodPubSub(p); err != nil {
			fmt.Printf("running runTestPubSub failed with: %s\n", err)
			return 1
		}

		return 0
	}

	h, err := e.HTTP()
	if err == nil {
		if err := runTestMethodPublishPubSub(h); err != nil {
			h.Write([]byte(fmt.Sprintf("Running runTestPublishPubSub failed with: %s", err)))
			return 1
		}
	}

	return 0
}

func runTestMethodPubSub(p pubsubEvent.Event) error {
	data, err := p.Data()
	if err != nil {
		return err
	}

	if string(data) != "Hello, world" {
		return errors.New("didn't get expected data")
	}

	return nil
}

func runTestMethodPublishPubSub(h httpEvent.Event) error {
	ch, err := pubsub.Channel("someChannel")
	if err != nil {
		return err
	}

	err = ch.Subscribe()
	if err != nil {
		return err
	}

	err = ch.Publish([]byte("Hello, world"))
	if err != nil {
		return err
	}

	_, err = h.Write([]byte("Success"))
	return err
}
