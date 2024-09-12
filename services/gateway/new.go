package gateway

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/clients/p2p/seer"
	substrate "github.com/taubyte/tau/clients/p2p/substrate"
	tauConfig "github.com/taubyte/tau/config"
	iface "github.com/taubyte/tau/core/services/gateway"
	seerIface "github.com/taubyte/tau/core/services/seer"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.gateway.service")

// TODO: Substrate Nodes should ping Gateway that they are alive, threshold should
func New(ctx context.Context, config *tauConfig.Node) (gateway iface.Service, err error) {
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
			return nil, fmt.Errorf("new node failed with: %w", err)
		}
	} else {
		g.node = config.Node
	}

	clientNode := g.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	if err = g.startHttp(config); err != nil { // should start at the end
		return nil, fmt.Errorf("starting http failed with: %w", err)
	}

	if config.Http == nil {
		g.http.Start()
	}

	if len(config.P2PAnnounce) < 1 {
		return nil, errors.New("P2P Announce is empty")
	}

	if g.substrateClient, err = substrate.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new streams client failed with: %w", err)
	}

	sc, err := seer.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("new seer client failed with: %w", err)
	}

	if err = protocolCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeGateway); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	g.attach()
	return g, nil
}
