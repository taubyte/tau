package monkey

import (
	"context"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	ci "github.com/taubyte/go-simple-container/gc"
	"github.com/taubyte/tau/clients/p2p/hoarder"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	seerIface "github.com/taubyte/tau/core/services/seer"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tauConfig "github.com/taubyte/tau/config"
	patrickSpecs "github.com/taubyte/tau/pkg/specs/patrick"

	streams "github.com/taubyte/tau/p2p/streams/service"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var logger = log.Logger("tau.monkey.service")

func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		patrickSpecs.PubSubIdent,
		func(msg *pubsub.Message) {
			go srv.pubsubMsgHandler(msg)
		},
		func(err error) {
			// re-establish if fails
			if err.Error() != "context canceled" {
				logger.Error("Subscription had an error:", err.Error())
				if err := srv.subscribe(); err != nil {
					logger.Error("resubscribe failed with:", err.Error())
				}
			}
		},
	)
}

func New(ctx context.Context, config *tauConfig.Node) (*Service, error) {
	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	srv := &Service{
		ctx:    ctx,
		dev:    config.DevMode,
		config: config,
	}

	err = ci.Start(ctx, ci.Interval(ci.DefaultInterval), ci.MaxAge(ci.DefaultMaxAge))
	if err != nil {
		return nil, err
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, config, path.Join(config.Root, protocolCommon.Monkey))
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = config.Node
		srv.dvPublicKey = config.DomainValidation.PublicKey
	}

	srv.clientNode = srv.node
	if config.ClientNode != nil {
		srv.clientNode = config.ClientNode
	}

	// should end if any of the two contexts ends
	err = srv.subscribe()
	if err != nil {
		return nil, err
	}

	srv.stream, err = streams.New(srv.node, protocolCommon.Monkey, protocolCommon.MonkeyProtocol)
	if err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()

	sc, err := seerClient.New(ctx, srv.clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = protocolCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeMonkey)
	if err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with %s", err)
	}

	srv.monkeys = make(map[string]*Monkey, 0)

	srv.patrickClient, err = NewPatrick(ctx, srv.clientNode)
	if err != nil {
		return nil, err
	}

	srv.tnsClient, err = tnsClient.New(ctx, srv.clientNode)
	if err != nil {
		return nil, err
	}

	srv.hoarderClient, err = hoarder.New(ctx, srv.clientNode)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Monkey)
	defer logger.Info(protocolCommon.Monkey, "closed")

	srv.stream.Stop()

	srv.tnsClient.Close()
	srv.patrickClient.Close()

	return nil
}
