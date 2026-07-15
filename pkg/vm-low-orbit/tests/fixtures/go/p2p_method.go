//go:build p2p_method

package main

//lint:file-ignore U1000 compiled file

import (
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/common"
	"github.com/taubyte/go-sdk/event"
	httpEvent "github.com/taubyte/go-sdk/http/event"
	p2pEvent "github.com/taubyte/go-sdk/p2p/event"
	"github.com/taubyte/go-sdk/p2p/node"
)

//export methodP2P
func methodP2P(e event.Event) uint32 {
	if e.Type() == common.EventTypeP2P {
		p, err := e.P2P()
		if err != nil {
			fmt.Println("ERR", err)
			return 1
		}

		if err := runTestP2PMethod(p); err != nil {
			p.Write([]byte(fmt.Sprintf("Running runTestP2P failed with: %s", err)))
			return 1
		}

		return 0
	}

	h, err := e.HTTP()
	if err == nil {
		if err := runTestPublishP2PMethod(h); err != nil {
			h.Write([]byte(fmt.Sprintf("Running runTestPublishP2P failed with: %s", err)))
			return 1
		}
	}

	return 0
}

func runTestP2PMethod(p p2pEvent.Event) error {
	data, err := p.Data()
	if err != nil {
		return err
	}

	if string(data) != "Hello, world" {
		return errors.New("didn't get expected data")
	}

	err = p.Write([]byte("Hello from the other side"))
	if err != nil {
		return err
	}

	return nil
}

func runTestPublishP2PMethod(h httpEvent.Event) error {
	cmd, err := node.New("/testproto/v1").Command("someCommand")
	if err != nil {
		return err
	}

	data, err := cmd.Send([]byte("Hello, world"))
	if err != nil {
		return err
	}

	_, err = h.Write(data)
	return err
}
