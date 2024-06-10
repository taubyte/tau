package hoarder

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/fxamacker/cbor/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	seerIface "github.com/taubyte/tau/core/services/seer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	"github.com/taubyte/tau/pkg/kvdb"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
	protocolCommon "github.com/taubyte/tau/services/common"
)

func New(ctx context.Context, config *tauConfig.Node) (service hoarderIface.Service, err error) {
	if config == nil {
		config = &tauConfig.Node{}
	}

	if err = config.Validate(); err != nil {
		err = fmt.Errorf("validating node config failed with: %w", err)
		return
	}

	s := &Service{
		auctions:       make(auctionStore),
		auctionHistory: make(auctionHistory),
		lotteryPool:    make(lotteryPool),
	}

	defer func() {
		if err != nil {
			logger.Errorf("starting hoarder service failed with: %s", err.Error())
			s.Close()
		}
	}()

	// TODO move database root to new
	if config.Node == nil {
		s.node, err = tauConfig.NewNode(ctx, config, path.Join(config.Root, protocolCommon.Hoarder))
		if err != nil {
			return nil, fmt.Errorf("new peer node failed with: %w", err)

		}
	} else {
		s.node = config.Node
	}

	clientNode := s.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	if s.stream, err = streams.New(s.node, protocolCommon.Hoarder, protocolCommon.HoarderProtocol); err != nil {
		return nil, fmt.Errorf("new command service failed with: %w", err)
	}

	s.dbFactory = config.Databases
	if s.dbFactory == nil {
		s.dbFactory = kvdb.New(s.node)
	}

	if s.db, err = s.dbFactory.New(logger, protocolCommon.Hoarder, 5); err != nil {
		return nil, fmt.Errorf("creating database failed with: %w", err)
	}

	s.setupStreamRoutes()

	if err = s.subscribe(ctx); err != nil {
		return nil, fmt.Errorf("pubsub subscribe failed with: %w", err)
	}

	if s.tnsClient, err = tnsApi.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("creating new tns client failed with: %w", err)
	}

	// TODO: caching this why? StartSeerBeacon should handle this
	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("new seer client failed with: %w", err)
	}

	if err = protocolCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeHoarder); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	service = s
	return
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Hoarder)
	defer logger.Info(protocolCommon.Hoarder, "closed")
	if srv.stream != nil {
		srv.stream.Stop()
	}

	if srv.tnsClient != nil {
		srv.tnsClient.Close()
	}
	if srv.db != nil {
		srv.db.Close()
	}

	*srv = Service{node: srv.node}
	return nil
}

// This only handles incoming new request for orders
func (srv *Service) subscribe(ctx context.Context) error {
	return srv.node.PubSubSubscribe(
		hoarderSpecs.PubSubIdent,
		func(msg *pubsub.Message) {
			auction := new(hoarderIface.Auction)
			var err error
			defer func() {
				if err != nil {
					logger.Error("handling auction failed with: ", err.Error())
				}
			}()
			if err = cbor.Unmarshal(msg.Data, auction); err != nil {
				err = fmt.Errorf("unmarshal failed with: %w", err)
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
		},
		func(err error) {
			if !errors.Is(err, context.Canceled) {
				logger.Error("subscription ended with error:", err.Error())
				logger.Info("re-establishing subscription")
				if err := srv.subscribe(ctx); err != nil {
					logger.Error("resubscribe failed with:", err.Error())
				}
			}
		},
	)
}
