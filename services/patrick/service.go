package service

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/ipfs/go-log/v2"
	authAPI "github.com/taubyte/tau/clients/p2p/auth"
	monkeyApi "github.com/taubyte/tau/clients/p2p/monkey"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	seerIface "github.com/taubyte/tau/core/services/seer"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/kvdb"
	servicesCommon "github.com/taubyte/tau/services/common"

	kvdbIface "github.com/taubyte/tau/core/kvdb"

	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
)

var (
	BootstrapTime            = 10 * time.Second
	logger                   = log.Logger("tau.patrick.service")
	DefaultReAnnounceJobTime = 1 * time.Minute
	MaxReAnnounceJobs        = 10
)

func New(ctx context.Context, config *tauConfig.Node) (*PatrickService, error) {
	var srv PatrickService

	srv.ctx, srv.cancel = context.WithCancel(ctx)

	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	srv.devMode = config.DevMode

	if config.Node == nil {
		srv.node, err = tauConfig.NewNode(srv.ctx, config, path.Join(config.Root, servicesCommon.Patrick))
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

	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	// Auth Consumer/Client
	srv.authClient, err = authAPI.New(srv.ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed auth api new with error: %w", err)
	}

	srv.tnsClient, err = tnsApi.New(ctx, clientNode)
	if err != nil {
		return nil, err
	}

	srv.monkeyClient, err = monkeyApi.New(srv.ctx, clientNode)
	if err != nil {
		return nil, err
	}

	// Create a database to store the jobs in
	srv.db, err = srv.dbFactory.New(logger, servicesCommon.Patrick, 5)
	if err != nil {
		return nil, fmt.Errorf("failed kv new with error: %w", err)
	}

	srv.stream, err = streams.New(srv.node, servicesCommon.Patrick, servicesCommon.PatrickProtocol)
	if err != nil {
		return nil, fmt.Errorf("failed stream new with error: %w", err)
	}

	srv.hostUrl = config.NetworkFqdn
	srv.setupStreamRoutes()
	srv.stream.Start()

	// HTTP
	if config.Http == nil {
		srv.http, err = auto.New(srv.ctx, srv.node, config)
		if err != nil {
			return nil, err
		}
	} else {
		srv.http = config.Http
	}

	srv.setupHTTPRoutes()

	if config.Http == nil {
		srv.http.Start()
	}

	sc, err := seerClient.New(srv.ctx, clientNode, config.SensorsRegistry())
	if err != nil {
		return nil, fmt.Errorf("failed creating seer client %v", err)
	}

	err = servicesCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypePatrick)
	if err != nil {
		return nil, err
	}

	// Go routine to re announce any pending jobs
	go func() {
		if srv.devMode {
			DefaultReAnnounceJobTime = 5 * time.Second
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(DefaultReAnnounceJobTime):
				_ctx, cancel := context.WithTimeout(ctx, DefaultReAnnounceJobTime)
				err := srv.ReannounceJobs(_ctx)
				if err != nil {
					logger.Error(err)
				}
				cancel()
			}
		}
	}()

	return &srv, nil
}

func (s *PatrickService) KV() kvdbIface.KVDB {
	return s.db
}

func (s *PatrickService) Node() peer.Node {
	return s.node
}

func (srv *PatrickService) Close() error {
	logger.Info("Closing", servicesCommon.Patrick)
	defer logger.Info(servicesCommon.Patrick, "closed")

	srv.cancel()

	srv.stream.Stop()
	srv.db.Close()

	return nil
}
