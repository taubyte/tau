package gateway

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/clients/p2p/seer"
	substrate "github.com/taubyte/tau/clients/p2p/substrate"
	iface "github.com/taubyte/tau/core/services/gateway"
	seerIface "github.com/taubyte/tau/core/services/seer"
	tauConfig "github.com/taubyte/tau/pkg/config"
	auto "github.com/taubyte/tau/pkg/http-auto"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.gateway.service")

func New(ctx context.Context, cfg tauConfig.Config) (gateway iface.Service, err error) {
	g := &Gateway{
		ctx: ctx,
	}

	g.dev, g.verbose = cfg.DevMode(), cfg.Verbose()
	g.cluster = cfg.Cluster()

	if g.node = cfg.Node(); g.node == nil {
		if g.node, err = tauConfig.NewNode(ctx, cfg, path.Join(cfg.Root(), protocolCommon.Gateway)); err != nil {
			return nil, fmt.Errorf("new node failed with: %w", err)
		}
	}

	clientNode := g.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}

	if g.http = cfg.Http(); g.http == nil {
		if g.http, err = auto.New(g.ctx, g.node, cfg); err != nil {
			return nil, fmt.Errorf("starting http failed with: %w", err)
		}
		defer g.http.Start()
	}

	if len(cfg.P2PAnnounce()) < 1 {
		return nil, errors.New("P2P Announce is empty")
	}

	if g.substrateClient, err = substrate.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new streams client failed with: %w", err)
	}

	sc, err := seer.New(ctx, clientNode, nil)
	if err != nil {
		return nil, fmt.Errorf("new seer client failed with: %w", err)
	}

	if err = protocolCommon.StartSeerBeacon(cfg, sc, seerIface.ServiceTypeGateway); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	g.attach()
	return g, nil
}
