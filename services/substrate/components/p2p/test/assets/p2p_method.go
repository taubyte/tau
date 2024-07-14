package lib

import (
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
	p2pEvent "github.com/taubyte/go-sdk/p2p/event"
	"github.com/taubyte/go-sdk/p2p/node"
)

//export methodP2P
func MethodP2P(e event.Event) uint32 {
	p, err := e.P2P()
	if err != nil {
		fmt.Println("ERR", err)
		return 1
	}

	if err := RunTestP2P(p); err != nil {
		p.Write([]byte(fmt.Sprintf("Running runTestP2P failed with: %s", err)))
		return 1
	}

	return 0

}

func RunTestP2P(p p2pEvent.Event) error {
	data, err := p.Data()
	if err != nil {
		return err
	}

	if string(data) != "Hello, world" {
		return errors.New("didn't get expected data")
	}

	err = p.Write([]byte("hello from the other side"))
	if err != nil {
		return err
	}

	return nil
}

func RunTestPublishP2P(h http.Event) error {
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
