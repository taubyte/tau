package service

import (
	"context"
	"fmt"
	"testing"

	keypair "github.com/taubyte/tau/p2p/keypair"
	"github.com/taubyte/tau/p2p/streams"

	peer "github.com/taubyte/tau/p2p/peer"

	"github.com/taubyte/tau/p2p/streams/command"
	cr "github.com/taubyte/tau/p2p/streams/command/response"
)

func TestNewService(t *testing.T) {
	ctx := context.Background()

	p1, err := peer.New( // provider
		ctx,
		nil,
		keypair.NewRaw(),
		nil,
		[]string{fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", 11001)},
		nil,
		true,
		false,
	)
	if err != nil {
		t.Errorf("Peer creation returned error `%s`", err.Error())
		return
	}

	svr, err := New(p1, "hello", "/hello/1.0")
	if err != nil {
		t.Errorf("Service creation returned error `%s`", err.Error())
	}
	svr.Define("hi", func(context.Context, streams.Connection, command.Body) (cr.Response, error) {
		return cr.Response{"message": "HI"}, nil
	})
}
