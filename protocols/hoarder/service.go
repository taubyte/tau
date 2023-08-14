package hoarder

import (
	"context"
	"fmt"
	"path"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-log/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	hoarderIface "github.com/taubyte/go-interfaces/services/hoarder"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	hoarderSpecs "github.com/taubyte/go-specs/hoarder"
	streams "github.com/taubyte/p2p/streams/service"
	seer_client "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/pkgs/kvdb"
	protocolCommon "github.com/taubyte/tau/protocols/common"
)

var (
	logger = log.Logger("hoarder.service")
)

func New(ctx context.Context, config *tauConfig.Node) (*Service, error) {
	var srv Service
	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("config build failed with: %s", err)
	}

	srv.auctions = make(auctionStore)
	srv.auctionHistory = make(auctionHistory)
	srv.lotteryPool = make(lotteryPool)

	// TODO move database root to new
	if config.Node == nil {
		srv.node, err = tauConfig.NewNode(ctx, config, path.Join(config.Root, protocolCommon.Hoarder))
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

	srv.dbFactory = config.Databases
	if srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	srv.db, err = srv.dbFactory.New(logger, protocolCommon.Auth, 5)
	if err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()
	if err = srv.subscribe(ctx); err != nil {
		return nil, fmt.Errorf("subscribe failed with: %s", err)
	}

	if srv.tnsClient, err = tnsApi.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new tns client failed with: %s", err)
	}

	sc, err := seer_client.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("new seer client failed with: %s", err)
	}

	if err = protocolCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeHoarder); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	return &srv, nil
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Hoarder)
	defer logger.Info(protocolCommon.Hoarder, "closed")
	srv.stream.Stop()
	srv.tnsClient.Close()
	srv.db.Close()

	return nil
}

// This only handles incoming new request for orders
func (srv *Service) subscribe(ctx context.Context) error {
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
				err = srv.auctionNew(ctx, auction, msg)
			case hoarderIface.AuctionIntent:
				err = srv.auctionIntent(auction, msg)
			case hoarderIface.AuctionEnd:
				err = srv.auctionEnd(ctx, auction, msg)
			}

			if err != nil {
				return
			}
		},

		func(err error) {
			// re-establish if fails
			if err.Error() != "context canceled" {
				logger.Error("Subscription had an error:", err.Error())

				if err := srv.subscribe(ctx); err != nil {
					logger.Error("resubscribe failed with:", err.Error())
				}
			}
		},
	)
}
