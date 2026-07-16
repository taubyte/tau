//go:build p2p

package main

//lint:file-ignore U1000 compiled file

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taubyte/go-sdk/common"
	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
	"github.com/taubyte/go-sdk/p2p/node"
)

type PassedData struct {
	Sent      string `json:"something_sent"`
	Responded string `json:"something_responded"`
	From      string `json:"from"`
}

//export callp2p
func callp2p(e event.Event) uint32 {
	if e.Type() == common.EventTypeP2P {
		if err := runTestP2P(e); err != nil {
			panic(fmt.Sprintf("runTestP2P failed with %s", err))
		}

		return 0
	}

	h, err := e.HTTP()
	if err == nil {
		if err := runTestPublishP2P(h); err != nil {
			panic(fmt.Sprintf("runTestPublishP2P failed with %s", err))
		}
	}

	return 0
}

func runTestP2P(e event.Event) error {
	p, err := e.P2P()
	if err != nil {
		return err
	}

	from, err := p.From()
	if err != nil {
		return err
	}

	data, err := p.Data()
	if err != nil {
		return err
	}

	pd := &PassedData{}
	if err := json.Unmarshal(data, pd); err != nil {
		return err
	}

	if pd.Sent != "Hello, world!" {
		return errors.New("didn't get expected data")
	}
	pd.Responded = "Hello from the other side"
	pd.From = from.String()

	j, err := json.Marshal(pd)
	if err != nil {
		return err
	}

	return p.Write(j)
}

func runTestPublishP2P(h http.Event) error {
	cmd, err := node.New("/testproto/v1").Command("someCommand")
	if err != nil {
		return err
	}

	j, err := json.Marshal(&PassedData{Sent: "Hello, world!"})
	if err != nil {
		return err
	}

	data, err := cmd.Send(j)
	if err != nil {
		return err
	}

	_, err = h.Write(data)
	return err
}
