//go:build p2p

package main

//lint:file-ignore U1000 compiled file

import (
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-sdk/common"
	"github.com/taubyte/go-sdk/event"
	http "github.com/taubyte/go-sdk/http/event"
	"github.com/taubyte/go-sdk/p2p/node"
)

//go:generate go get github.com/mailru/easyjson
//go:generate go install github.com/mailru/easyjson/...@latest
//go:generate easyjson -all ${GOFILE}

type PassedData struct {
	Sent      string  `json:"something_sent"`
	Responded string  `json:"something_responded"`
	From      cid.Cid `json:"from"`
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

	cid, err := p.From()
	if err != nil {
		return err
	}

	data, err := p.Data()
	if err != nil {
		return err
	}

	pd := &PassedData{}
	err = pd.UnmarshalJSON(data)
	if err != nil {
		return err
	}

	if pd.Sent != "Hello, world!" {
		return errors.New("didn't get expected data")
	}
	pd.Responded = "Hello from the other side"
	pd.From = cid

	j, err := pd.MarshalJSON()
	if err != nil {
		return err
	}

	err = p.Write(j)
	if err != nil {
		return err
	}

	return nil
}

func runTestPublishP2P(h http.Event) error {
	cmd, err := node.New("/testproto/v1").Command("someCommand")
	if err != nil {
		return err
	}

	pd := &PassedData{
		Sent: "Hello, world!",
	}

	j, err := pd.MarshalJSON()
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
