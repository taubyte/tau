//go:build multi_pubsub

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"

	"github.com/taubyte/go-sdk/event"
	"github.com/taubyte/go-sdk/http/client"
)

//export multi_pubsubtest
func multi_pubsubtest(e event.Event) uint32 {
	if err := runTestMultiPubSub(e); err != nil {
		panic(fmt.Sprintf("runTestPubSub failed with %s", err))
	}

	return 0
}

func runTestMultiPubSub(e event.Event) error {
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

		req, err := c.Request("http://localhost:9090/multi_pubsub", client.Method("POST"))
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
