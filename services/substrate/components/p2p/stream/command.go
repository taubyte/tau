package stream

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p/core/peer"
	iface "github.com/taubyte/tau/core/services/substrate/components/p2p"
	"github.com/taubyte/tau/p2p/streams/client"
	"github.com/taubyte/tau/p2p/streams/command"
	"github.com/taubyte/tau/p2p/streams/command/response"
	"github.com/taubyte/tau/services/substrate/components/p2p/common"
)

type Command struct {
	matcher *iface.MatchDefinition
	client  *client.Client
}

func (st *Stream) Command(command string) (iface.Command, error) {
	if len(command) == 0 {
		return nil, errors.New("cannot send an empty command")
	}

	st.matcher.Command = command
	return &Command{
		matcher: st.matcher,
		client:  st.client,
	}, nil
}

func (c *Command) beforeSend(body command.Body) (*client.Client, command.Body, error) {
	data, ok := body["data"]
	if !ok {
		return nil, nil, fmt.Errorf("no data found in body")
	}

	return c.client, command.Body{
		"matcher": c.matcher,
		"data":    data,
	}, nil
}

func (c *Command) Send(ctx context.Context, body map[string]interface{}) (response.Response, error) {
	p2pClient, body, err := c.beforeSend(body)
	if err != nil {
		return nil, err
	}

	resp, err := p2pClient.Send(c.matcher.Command, body)
	if err != nil {
		common.Logger.Errorf("sending command %s failed with: %s", c.matcher.Command, err.Error())
	}

	return resp, err
}

func (c *Command) SendTo(ctx context.Context, cid cid.Cid, body map[string]interface{}) (response.Response, error) {
	p2pClient, body, err := c.beforeSend(body)
	if err != nil {
		return nil, err
	}

	pid, err := peer.FromCid(cid)
	if err != nil {
		return nil, fmt.Errorf("cid to pid failed with: %w", err)
	}

	resp, err := p2pClient.Send(c.matcher.Command, body, pid)
	if err != nil {
		common.Logger.Errorf("sending command %s to %s failed with: %s", c.matcher.Command, pid, err.Error())
	}

	return resp, err
}
