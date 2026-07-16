//go:build pubsub

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/common"
	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/http/client"
	pubsub "github.com/taubyte/go-sdk/pubsub/node"
)

//export pubsubtest
func pubsubtest(e event.Event) uint32 {
	if e.Type() == common.EventTypePubsub {
		if err := runTestPubSub(e); err != nil {
			panic(fmt.Sprintf("runTestPubSub failed with %s", err))
		}

		return 0
	}

	h, err := e.HTTP()
	if err == nil {
		query, _ := h.Query().Get("name")
		if query == "pubstuff" {
			if err := runTestAttachPubsub(e); err != nil {
				panic(fmt.Sprintf("runTestPubSub failed with %s", err))
			}
		}
		if query == "actuallypublish" {
			if err := runTestPublish(e); err != nil {
				panic(fmt.Sprintf("runTestPublish failed with %s", err))
			}
		}
	}

	return 0
}

func runTestPubSub(e event.Event) error {
	p, err := e.PubSub()
	if err != nil {
		return err
	}

	channel, err := p.Channel()
	if err != nil {
		return err
	}

	expectedChannelName := "someChannel"
	if channel.Name() != expectedChannelName {
		return fmt.Errorf("incorrect channel got: %s expected: %s", channel.Name(), expectedChannelName)
	}

	data, err := p.Data()
	if err != nil {
		return err
	}

	if string(data) == "Hello, world" {
		c, err := client.New()
		if err != nil {
			return fmt.Errorf("create client failed with: %v", err)
		}

		req, err := c.Request("http://localhost:9090/pubsub", client.Method("POST"))
		if err != nil {
			return fmt.Errorf("create request failed with: %v", err)
		}

		resp, err := req.Do()
		if err != nil {
			return fmt.Errorf("do request failed with: %v", err)
		}
		defer resp.Body().Close()
	}

	return nil
}

func runTestPublish(e event.Event) error {
	channel, err := pubsub.Channel("someChannel")
	if err != nil {
		return err
	}

	err = channel.Publish([]byte("Hello, world"))
	if err != nil {
		return err
	}

	return nil
}

func runTestAttachPubsub(e event.Event) error {
	channel, err := pubsub.Channel("someChannel")
	if err != nil {
		return err
	}

	err = channel.Subscribe()
	if err != nil {
		return err
	}

	return nil
}
