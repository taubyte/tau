//go:build p2p_discover_send_to

package main

//lint:file-ignore U1000 compiled file

import (
	"fmt"
	"time"

	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
	p2p "github.com/taubyte/go-sdk/p2p/event"
	"github.com/taubyte/go-sdk/p2p/node"
)

//export pingme
func sendTo(e event.Event) uint32 {
	h, err := e.HTTP()
	if err != nil {
		return 1
	}

	err = runPing2(h)
	if err != nil {
		errString := fmt.Sprintf(`{"error": ping failed with %s}`, err)
		h.Write([]byte(errString))
		return 1
	}

	return 0
}

func runPing2(h http.Event) error {
	cmd, err := node.New("/test/v1").Command("ping")
	if err != nil {
		return err
	}

	peers, err := node.Discover(1, time.Second*2)
	if err != nil {
		return err
	}

	response, err := cmd.SendTo([]byte("Hello, world"), peers[0])
	if err != nil {
		return err
	}

	_, err = h.Write(response)
	return err
}

//export pingp2p
func hitByPing(e event.Event) uint32 {
	p, err := e.P2P()
	if err != nil {
		return 1
	}

	err = runPingP2P2(p)
	if err != nil {
		errString := fmt.Sprintf(`{"error": ping failed with %s}`, err)
		p.Write([]byte(errString))
		return 1
	}

	return 0
}

func runPingP2P2(e p2p.Event) error {
	command, err := e.Command()
	if err != nil {
		return err
	}

	data, err := e.Data()
	if err != nil {
		return err
	}

	from, err := e.From()
	if err != nil {
		return err
	}

	protocol, err := e.Protocol()
	if err != nil {
		return err
	}

	to, err := e.To()
	if err != nil {
		return err
	}

	toWrite := fmt.Sprintf(`{
	"protocol": "%s",
	"command": "%s",
	"data": "%s",
	"from": "%s",
	"to": "%s"
}`, protocol, command, string(data), from.String(), to.String())

	return e.Write([]byte(toWrite))
}
