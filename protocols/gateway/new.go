package gateway

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/p2p/streams/client"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

var logger = log.Logger("gateway.service")

func New(ctx context.Context, config *tauConfig.Node) (gateway *Gateway, err error) {
	g := &Gateway{
		ctx: ctx,
	}

	if config == nil {
		config = &tauConfig.Node{}
	}

	if err = config.Validate(); err != nil {
		return
	}

	g.dev, g.verbose = config.DevMode, config.Verbose

	if config.Node == nil {
		if g.node, err = tauConfig.NewNode(ctx, config, path.Join(config.Root, protocolCommon.Gateway)); err != nil {
			return nil, fmt.Errorf("new lite node failed with: %w", err)
		}
	} else {
		g.node = config.Node
	}

	// For Odo
	clientNode := g.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	if err = g.startHttp(config); err != nil {
		return nil, fmt.Errorf("starting http failed with: %w", err)
	}

	if g.tns, err = tnsClient.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new tns client failed with: %w", err)
	}

	if config.Http == nil {
		g.http.Start()
	}

	// ???
	if len(config.P2PAnnounce) < 1 {
		return nil, errors.New("P2P Announce is empty")
	}

	if g.p2pClient, err = client.New(ctx, g.node, nil, protocolCommon.GatewayProtocol, MinPeers, MaxPeers); err != nil {
		return nil, fmt.Errorf("new streams client failed with: %w", err)
	}

	g.attach()

	return g, nil
}

// This is how we are doing it for clients/p2p/tns
// But shouldnt we parsing from config how many tns there are?
var (
	MinPeers = 0
	MaxPeers = 4
)
