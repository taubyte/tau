//go:build p2p_listen

package main

//lint:file-ignore U1000 compiled file

import (
	"encoding/json"
	"fmt"

	"github.com/taubyte/go-sdk/event"
	httpClient "github.com/taubyte/go-sdk/http/client"
	http "github.com/taubyte/go-sdk/http/event"
	"github.com/taubyte/go-sdk/p2p/node"
)

type PassedDataP2PListen struct {
	Sent []byte `json:"something_sent"`
	From string `json:"from"`
}

//export callp2pListenCall
func callp2pListenCall(e event.Event) uint32 {
	p, err := e.P2P()
	if err != nil {
		return 1
	}

	from, err := p.From()
	if err != nil {
		return 1
	}

	data, err := p.Data()
	if err != nil {
		return 1
	}

	pd := &PassedDataP2PListen{
		From: from.String(),
		Sent: data,
	}
	j, err := json.Marshal(pd)
	if err != nil {
		return 1
	}

	c, err := httpClient.New()
	if err != nil {
		return 1
	}

	req, err := c.Request("http://localhost:9090/p2p_listen", httpClient.Method("POST"), httpClient.Body(j))
	if err != nil {
		return 1
	}

	resp, err := req.Do()
	if err != nil {
		return 1
	}
	defer resp.Body().Close()

	return 0
}

//export callp2pListen
func callp2pListen(e event.Event) uint32 {
	h, err := e.HTTP()
	if err == nil {
		if err := runTestP2PListen(h); err != nil {
			h.Write([]byte(fmt.Sprintf("runTestP2PListen failed with %s", err)))
			return 1
		}
	}

	return 0
}

func runTestP2PListen(h http.Event) error {
	projectProtocol, err := node.New("/testproto/v1").Listen()
	if err != nil {
		return err
	}

	_, err = h.Write([]byte(projectProtocol))
	if err != nil {
		return err
	}

	return nil
}
