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
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/components/p2p/common"
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
	p2pClient, err := client.New(c.srv.Node(), protocolCommon.SubstrateP2PProtocol)
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

func (c *Command) Send(ctx context.Context, body map[string]interface{}) (response.Response, error) {
	p2pClient, body, err := c.beforeSend(ctx, body)
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
	p2pClient, body, err := c.beforeSend(ctx, body)
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
