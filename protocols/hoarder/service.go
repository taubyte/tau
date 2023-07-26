package hoarder

import (
	"context"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	pebble "github.com/ipfs/go-ds-pebble"
	"github.com/ipfs/go-log/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
	seer_client "github.com/taubyte/odo/clients/p2p/seer"
	tnsApi "github.com/taubyte/odo/clients/p2p/tns"
	odoConfig "github.com/taubyte/odo/config"
	"github.com/taubyte/odo/protocols/common"
	protocolCommon "github.com/taubyte/odo/protocols/common"
	streams "github.com/taubyte/p2p/streams/service"
)

var (
	logger = log.Logger("hoarder.service")
)

func New(ctx context.Context, config *odoConfig.Protocol) (*Service, error) {
	var srv Service
	srv.ctx = ctx

	if config == nil {
		config = &odoConfig.Protocol{}
	}

	err := config.Build(odoConfig.ConfigBuilder{
		DefaultP2PListenPort: protocolCommon.HoarderDefaultP2PListenPort,
	})
	if err != nil {
		return nil, fmt.Errorf("config build failed with: %s", err)
	}

	srv.createMaps()

	// TODO move database root to new
	if config.Node == nil {
		srv.node, err = odoConfig.NewNode(ctx, config, protocolCommon.Hoarder)
		if err != nil {
			return nil, fmt.Errorf("config new node failed with: %s", err)
		}
	} else {
		srv.node = config.Node
	}

	// For Odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	if srv.stream, err = streams.New(srv.node, protocolCommon.Hoarder, protocolCommon.HoarderProtocol); err != nil {
		return nil, fmt.Errorf("new streams failed with: %s", err)
	}

	if srv.store, err = pebble.NewDatastore(config.Root, nil); err != nil {
		return nil, fmt.Errorf("creating pebble datastore failed with: %s", err)
	}

	srv.setupStreamRoutes()
	if err = srv.subscribe(); err != nil {
		return nil, fmt.Errorf("subscribe failed with: %s", err)
	}

	if srv.tnsClient, err = tnsApi.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new tns client failed with: %s", err)
	}

	sc, err := seer_client.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("new seer client failed with: %s", err)
	}

	if err = common.StartSeerBeacon(config, sc, seerIface.ServiceTypeHoarder); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	return &srv, nil
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Hoarder)
	defer logger.Info(protocolCommon.Hoarder, "closed")

	srv.stream.Stop()
	srv.tnsClient.Close()

	if srv.store != nil {
		srv.store.Close()
	}

	return nil
}

// This only handles incoming new request for orders
func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		hoarderSpecs.PubSubIdent,
		func(msg *pubsub.Message) {
			auction := new(hoarderIface.Auction)
			err := cbor.Unmarshal(msg.Data, auction)
			if err != nil {
				return
			}

			valid := srv.validateMsg(auction, msg)
			if !valid {
				return
			}

			switch auction.Type {
			case hoarderIface.AuctionNew:
				err = srv.auctionNew(auction, msg)
			case hoarderIface.AuctionIntent:
				err = srv.auctionIntent(auction, msg)
			case hoarderIface.AuctionEnd:
				err = srv.auctionEnd(auction, msg)
			}

			if err != nil {
				return
			}
		},

		func(err error) {
			// re-establish if fails
			if err.Error() != "context canceled" {
				logger.Errorf("Subscription had an error: %w", err)

				if err := srv.subscribe(); err != nil {
					logger.Errorf("resubscribe failed with: %w", err)
				}
			}
		},
	)
}
