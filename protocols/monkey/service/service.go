package service

import (
	"context"
	"fmt"
	"regexp"

	tnsClient "bitbucket.org/taubyte/tns-p2p-client"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	ci "github.com/taubyte/go-simple-container/gc"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	moody "bitbucket.org/taubyte/go-moody-blues"
	configutils "bitbucket.org/taubyte/p2p/config"
	streams "bitbucket.org/taubyte/p2p/streams/service"
	seerClient "bitbucket.org/taubyte/seer-p2p-client"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	domainSpecs "github.com/taubyte/go-specs/domain"
	patrickSpecs "github.com/taubyte/go-specs/patrick"
	protocolCommon "github.com/taubyte/odo/protocols/common"
)

var logger, _ = moody.New("monkey.service")

func (srv *Service) subscribe() error {
	return srv.node.PubSubSubscribe(
		patrickSpecs.PubSubIdent,
		func(msg *pubsub.Message) {
			go srv.pubsubMsgHandler(msg)
		},
		func(err error) {
			// re-establish if fails
			if err.Error() != "context canceled" {
				logger.Error(moodyCommon.Object{"msg": fmt.Sprintf("Subscription had an error: %s", err.Error())})
				if err := srv.subscribe(); err != nil {
					logger.Errorf("resubscribe failed with: %s", err)
				}
			}
		},
	)
}

func New(ctx context.Context, config *commonIface.GenericConfig) (*Service, error) {
	if config == nil {
		_cnf := &commonIface.GenericConfig{}
		_cnf.Bootstrap = true

		config = _cnf
	}

	err := config.Build(commonIface.ConfigBuilder{
		DefaultP2PListenPort: protocolCommon.MonkeyDefaultP2PListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, err
	}

	srv := &Service{
		ctx: ctx,
		dev: config.DevMode,
	}

	if commonIface.Deployment == commonIface.Odo {
		domainSpecs.SpecialDomain = regexp.MustCompile(config.Domains.Generated)
	}

	err = ci.Start(ctx, ci.Interval(ci.DefaultInterval), ci.MaxAge(ci.DefaultMaxAge))
	if err != nil {
		return nil, err
	}

	if config.Node == nil {
		srv.node, err = configutils.NewLiteNode(ctx, config, protocolCommon.Monkey)
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = config.Node
		srv.dvPublicKey = config.DVPublicKey
	}

	// For Odo
	if config.ClientNode != nil {
		srv.odoClientNode = config.ClientNode
	} else {
		srv.odoClientNode = srv.node
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

	sc, err := seerClient.New(ctx, srv.odoClientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = config.StartSeerBeacon(sc, seerIface.ServiceTypeMonkey)
	if err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with %s", err)
	}

	srv.monkeys = make(map[string]*Monkey, 0)

	srv.patrickClient, err = NewPatrick(ctx, srv.odoClientNode)
	if err != nil {
		return nil, err
	}

	srv.tnsClient, err = tnsClient.New(ctx, srv.odoClientNode)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func (srv *Service) Close() error {
	// TODO use debug logger
	fmt.Println("Closing", protocolCommon.Monkey)
	defer fmt.Println(protocolCommon.Monkey, "closed")

	// node.ctx
	srv.stream.Stop()

	// ctx & partly relies on node
	srv.tnsClient.Close()
	srv.patrickClient.Close()

	// ctx
	srv.node.Close()
	return nil
}
