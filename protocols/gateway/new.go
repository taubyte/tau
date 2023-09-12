package gateway

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync/atomic"
	"time"

	"github.com/ipfs/go-log/v2"
	iface "github.com/taubyte/go-interfaces/services/gateway"
	"github.com/taubyte/tau/clients/p2p/substrate"
	tauConfig "github.com/taubyte/tau/config"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

var logger = log.Logger("gateway.service")

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

	// For Odo
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

	// ???
	if len(config.P2PAnnounce) < 1 {
		return nil, errors.New("P2P Announce is empty")
	}

	if g.substrateClient, err = substrate.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new streams client failed with: %w", err)
	}

	// run once before go routine to verify FindPeers does not error
	if err = g.updatePeerCount(); err != nil {
		return nil, fmt.Errorf("update peer count failed with: %w", err)
	}

	// go func() {
	// 	ticker := time.NewTicker(UpdatePeerCountInterval)
	// 	for {
	// 		select {
	// 		case <-g.ctx.Done():
	// 			return
	// 		case <-ticker.C:
	// 			if err := g.updatePeerCount(); err != nil {
	// 				logger.Errorf("updating peer count failed with: %s", err.Error())
	// 			}
	// 		}
	// 	}
	// }()

	// above is not working
	g.connectedSubstrate = 4

	g.attach()

	return g, nil
}

var UpdatePeerCountInterval time.Duration = time.Hour

// This isnt even working
func (g *Gateway) updatePeerCount() error {
	peerDiscover, err := g.Node().Discovery().FindPeers(g.ctx, protocolCommon.SubstrateProtocol)
	if err != nil {
		return fmt.Errorf("finding substrate peers failed with: %w", err)
	}

	var count uint64
	for {
		_, ok := <-peerDiscover
		if ok {
			count++
		} else {
			break
		}
	}

	atomic.SwapUint64(&g.connectedSubstrate, count)
	return nil
}
