package service

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/ipfs/go-log/v2"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	authAPI "github.com/taubyte/tau/clients/p2p/auth"
	monkeyApi "github.com/taubyte/tau/clients/p2p/monkey"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	auto "github.com/taubyte/tau/pkgs/http-auto"
	"github.com/taubyte/tau/pkgs/kvdb"
	protocolsCommon "github.com/taubyte/tau/protocols/common"

	streams "github.com/taubyte/p2p/streams/service"
)

var (
	BootstrapTime                   = 10 * time.Second
	logger                          = log.Logger("patrick.Service")
	DefaultReAnnounceJobTime        = 7 * time.Minute
	DefaultReAnnounceFailedJobsTime = 7 * time.Minute
)

func New(ctx context.Context, config *tauConfig.Node) (*PatrickService, error) {
	var srv PatrickService

	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	srv.devMode = config.DevMode

	logger.Info(config)

	if config.Node == nil {
		srv.node, err = tauConfig.NewNode(ctx, config, path.Join(config.Root, protocolsCommon.Patrick))
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

	// For odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	// Auth Consumer/Client
	srv.authClient, err = authAPI.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed auth api new with error: %w", err)
	}

	srv.tnsClient, err = tnsApi.New(ctx, clientNode)
	if err != nil {
		return nil, err
	}

	srv.monkeyClient, err = monkeyApi.New(ctx, clientNode)
	if err != nil {
		return nil, err
	}

	// Create a database to store the jobs in
	srv.db, err = srv.dbFactory.New(logger, protocolsCommon.Patrick, 5)
	if err != nil {
		return nil, fmt.Errorf("failed kv new with error: %w", err)
	}

	srv.stream, err = streams.New(srv.node, protocolsCommon.Patrick, protocolsCommon.PatrickProtocol)
	if err != nil {
		return nil, fmt.Errorf("failed stream new with error: %w", err)
	}

	srv.hostUrl = config.NetworkFqdn
	srv.setupStreamRoutes()

	// HTTP
	if config.Http == nil {
		srv.http, err = auto.NewAuto(ctx, srv.node, config)
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

	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed creating seer client %v", err)
	}

	err = protocolsCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypePatrick)
	if err != nil {
		return nil, err
	}

	// Go routine to re announce any pending jobs
	go func() {
		if srv.devMode {
			DefaultReAnnounceJobTime = 5 * time.Second
			DefaultReAnnounceFailedJobsTime = 7 * time.Second
		}
		for {
			select {
			case <-time.After(DefaultReAnnounceJobTime):
				_ctx, cancel := context.WithTimeout(ctx, DefaultReAnnounceJobTime)
				err := srv.ReannounceJobs(_ctx)
				cancel()
				if err != nil {
					logger.Error(err)
				}
			case <-time.After(DefaultReAnnounceFailedJobsTime):
				_ctx, cancel := context.WithTimeout(ctx, DefaultReAnnounceFailedJobsTime)
				err := srv.ReannounceFailedJobs(_ctx)
				cancel()
				if err != nil {
					logger.Error(err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return &srv, nil
}

func (srv *PatrickService) Close() error {
	logger.Info("Closing", protocolsCommon.Patrick)
	defer logger.Info(protocolsCommon.Patrick, "closed")

	srv.stream.Stop()
	srv.tnsClient.Close()
	srv.authClient.Close()
	srv.monkeyClient.Close()
	srv.db.Close()

	return nil
}
