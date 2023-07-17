package stream

import (
	"context"
	"errors"
	"fmt"

	"bitbucket.org/taubyte/p2p/streams/client"
	"github.com/ipfs/go-cid"
	"github.com/taubyte/go-interfaces/moody"
	"github.com/taubyte/go-interfaces/p2p/streams"
	"github.com/taubyte/go-interfaces/services/substrate/p2p"
	iface "github.com/taubyte/go-interfaces/services/substrate/p2p"
	"github.com/taubyte/odo/protocols/node/components/p2p/common"
)

type Command struct {
	srv     iface.Service
	matcher *iface.MatchDefinition
}

func (st *Stream) Command(command string) (p2p.Command, error) {
	if len(command) == 0 {
		return nil, errors.New("Cannot send an empty command")
	}

	st.matcher.Command = command
	return &Command{
		srv:     st.srv,
		matcher: st.matcher,
	}, nil
}

func (c *Command) beforeSend(ctx context.Context, body streams.Body) (*client.Client, streams.Body, error) {
	// TODO srv.p2pClient
	p2pClient, err := client.New(ctx, c.srv.Node(), nil, common.Protocol, common.MinPeers, common.MaxPeers)
	if err != nil {
		return nil, nil, fmt.Errorf("New p2p client failed with: %s", err)
	}

	data, ok := body["data"]
	if ok == false {
		return nil, nil, fmt.Errorf("No data found in body")
	}

	return p2pClient, streams.Body{
		"matcher": c.matcher,
		"data":    data,
	}, nil
}

// TODO: should be in client
func (c *Command) Send(ctx context.Context, body map[string]interface{}) (streams.Response, error) {
	p2pClient, body, err := c.beforeSend(ctx, body)
	if err != nil {
		return nil, err
	}

	resp, err := p2pClient.Send(c.matcher.Command, body)
	if err != nil {
		c.srv.Logger().Error(moody.Object{"message": fmt.Sprintf("sending command %s failed with %s", c.matcher.Command, err)})
	}

	return resp, err
}

func (c *Command) SendTo(ctx context.Context, pid cid.Cid, body map[string]interface{}) (streams.Response, error) {
	p2pClient, body, err := c.beforeSend(ctx, body)
	if err != nil {
		return nil, err
	}

	resp, err := p2pClient.SendTo(pid, c.matcher.Command, body)
	if err != nil {
		c.srv.Logger().Error(moody.Object{"message": fmt.Sprintf("sending command %s to %s failed with %s", c.matcher.Command, pid, err)})
	}

	return resp, err
}
