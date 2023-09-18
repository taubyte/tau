package gateway

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	iface "github.com/taubyte/go-interfaces/services/gateway"
	substrate "github.com/taubyte/tau/clients/p2p/substrate/http"
	tauConfig "github.com/taubyte/tau/config"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

var logger = log.Logger("gateway.service")

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

	if err = g.startHttp(config); err != nil {
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

	g.attach()
	return g, nil
}
