package tns

import (
	"context"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	"github.com/taubyte/tau/core/services/seer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	"github.com/taubyte/tau/pkg/kvdb"

	tauConfig "github.com/taubyte/tau/pkg/config"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/tns/engine"
)

var (
	logger = log.Logger("tau.tns.service")
)

func New(ctx context.Context, cfg tauConfig.Config) (*Service, error) {
	srv := &Service{}

	var err error
	if srv.node = cfg.Node(); srv.node == nil {
		srv.node, err = tauConfig.NewNode(ctx, cfg, path.Join(cfg.Root(), servicesCommon.Tns))
		if err != nil {
			return nil, err
		}
	}

	if srv.dbFactory = cfg.Databases(); srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	if srv.db, err = srv.dbFactory.New(logger, servicesCommon.Tns, 5); err != nil {
		return nil, err
	}
	if srv.engine, err = engine.New(srv.db, engine.Prefix...); err != nil {
		return nil, err
	}
	if srv.stream, err = streams.New(srv.node, servicesCommon.Tns, servicesCommon.TnsProtocol); err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()
	srv.stream.Start()

	// For Odo
	clientNode := srv.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}
	var sc seer.Client
	if sc, err = seerClient.New(ctx, clientNode, cfg.SensorsRegistry()); err != nil {
		return nil, fmt.Errorf("failed creating seer client error: %v", err)
	}
	if err = servicesCommon.StartSeerBeacon(cfg, sc, seer.ServiceTypeTns); err != nil {
		return nil, err
	}

	return srv, nil
}

func (srv *Service) Close() error {
	// TODO use debug logger
	logger.Debugf("Closing %s", servicesCommon.Tns)
	defer logger.Debugf("%s closed", servicesCommon.Tns)

	// node.ctx
	srv.stream.Stop()

	srv.db.Close()

	return nil
}
