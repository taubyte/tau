package tns

import (
	"context"
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/services/seer"
	streams "github.com/taubyte/p2p/streams/service"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	"github.com/taubyte/tau/pkgs/kvdb"

	commonSpec "github.com/taubyte/go-specs/common"
	tauConfig "github.com/taubyte/tau/config"
	protocolsCommon "github.com/taubyte/tau/protocols/common"
	"github.com/taubyte/tau/protocols/tns/engine"
)

var (
	logger = log.Logger("tns.service")
)

func New(ctx context.Context, config *tauConfig.Node) (*Service, error) {
	srv := &Service{}

	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewNode(ctx, config, protocolsCommon.Tns)
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = config.Node
	}

	srv.dbFactory = config.Databases
	if srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	srv.db, err = srv.dbFactory.New(logger, protocolsCommon.Tns, 5)
	if err != nil {
		return nil, err
	}

	srv.engine, err = engine.New(srv.db, engine.Prefix...)
	if err != nil {
		return nil, err
	}

	// P2P
	srv.stream, err = streams.New(srv.node, protocolsCommon.Tns, commonSpec.TnsProtocol)
	if err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()

	// For Odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}
	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed creating seer client error: %v", err)
	}

	err = protocolsCommon.StartSeerBeacon(config, sc, seer.ServiceTypeTns)
	if err != nil {
		return nil, err
	}

	return srv, nil
}

func (srv *Service) Close() error {
	// TODO use debug logger
	logger.Info("Closing", protocolsCommon.Tns)
	defer logger.Info(protocolsCommon.Tns, "closed")

	// node.ctx
	srv.stream.Stop()

	srv.db.Close()

	return nil
}
