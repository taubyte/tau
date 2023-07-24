package stream

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	iface "github.com/taubyte/go-interfaces/services/substrate/components/p2p"
	"github.com/taubyte/odo/protocols/node/components/p2p/common"
	"github.com/taubyte/p2p/streams/client"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
)

type Command struct {
	srv     iface.Service
	matcher *iface.MatchDefinition
}

func (st *Stream) Command(command string) (iface.Command, error) {
	if len(command) == 0 {
		return nil, errors.New("cannot send an empty command")
	}

	st.matcher.Command = command
	return &Command{
		srv:     st.srv,
		matcher: st.matcher,
	}, nil
}

func (c *Command) beforeSend(ctx context.Context, body command.Body) (*client.Client, command.Body, error) {
	// TODO srv.p2pClient
	p2pClient, err := client.New(ctx, c.srv.Node(), nil, common.Protocol, common.MinPeers, common.MaxPeers)
	if err != nil {
		return nil, nil, fmt.Errorf("New p2p client failed with: %s", err)
	}

	data, ok := body["data"]
	if !ok {
		return nil, nil, fmt.Errorf("no data found in body")
	}

	return p2pClient, command.Body{
		"matcher": c.matcher,
		"data":    data,
	}, nil
}

// TODO: should be in client
func (c *Command) Send(ctx context.Context, body map[string]interface{}) (response.Response, error) {
	p2pClient, body, err := c.beforeSend(ctx, body)
	if err != nil {
		return nil, err
	}

	resp, err := p2pClient.Send(c.matcher.Command, body)
	if err != nil {
		common.Logger.Errorf("sending command %s failed with %w", c.matcher.Command, err)
	}

	return resp, err
}

func (c *Command) SendTo(ctx context.Context, pid cid.Cid, body map[string]interface{}) (response.Response, error) {
	p2pClient, body, err := c.beforeSend(ctx, body)
	if err != nil {
		return nil, err
	}

	resp, err := p2pClient.SendTo(pid, c.matcher.Command, body)
	if err != nil {
		common.Logger.Errorf("sending command %s to %s failed with: %w", c.matcher.Command, pid, err)
	}

	return resp, err
}
