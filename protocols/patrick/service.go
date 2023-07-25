package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ipfs/go-log/v2"
	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	authAPI "github.com/taubyte/odo/clients/p2p/auth"
	monkeyApi "github.com/taubyte/odo/clients/p2p/monkey"
	seerClient "github.com/taubyte/odo/clients/p2p/seer"
	tnsApi "github.com/taubyte/odo/clients/p2p/tns"
	"github.com/taubyte/odo/config"
	auto "github.com/taubyte/odo/pkgs/http-auto"
	kv "github.com/taubyte/odo/pkgs/kvdb/database"
	"github.com/taubyte/odo/protocols/common"
	protocolsCommon "github.com/taubyte/odo/protocols/common"

	streams "github.com/taubyte/p2p/streams/service"
)

var (
	BootstrapTime                   = 10 * time.Second
	logger                          = log.Logger("patrick.Service")
	DefaultReAnnounceJobTime        = 7 * time.Minute
	DefaultReAnnounceFailedJobsTime = 7 * time.Minute
)

func New(ctx context.Context, protocolConfig *config.Protocol) (*PatrickService, error) {
	var srv PatrickService

	if protocolConfig == nil {
		_cnf := &config.Protocol{}

		protocolConfig = _cnf
	}

	err := protocolConfig.Build(config.ConfigBuilder{
		DefaultP2PListenPort: protocolsCommon.PatrickDefaultP2PListenPort,
		DevHttpListenPort:    protocolsCommon.PatrickDevHttpListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	srv.devMode = protocolConfig.DevMode

	logger.Info(protocolConfig)

	if protocolConfig.Node == nil {
		srv.node, err = config.NewNode(ctx, protocolConfig, protocolsCommon.Patrick)
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = protocolConfig.Node
	}

	// For odo
	clientNode := srv.node
	if protocolConfig.ClientNode != nil {
		clientNode = protocolConfig.ClientNode
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
	srv.db, err = kv.New(logger, srv.node, protocolsCommon.Patrick, 5)
	if err != nil {
		return nil, fmt.Errorf("failed kv new with error: %w", err)
	}

	srv.stream, err = streams.New(srv.node, protocolsCommon.Patrick, protocolsCommon.PatrickProtocol)
	if err != nil {
		return nil, fmt.Errorf("failed stream new with error: %w", err)
	}

	srv.hostUrl = protocolConfig.NetworkUrl
	srv.setupStreamRoutes()

	// HTTP
	if protocolConfig.Http == nil {
		srv.http, err = auto.NewAuto(ctx, srv.node, protocolConfig)
		if err != nil {
			return nil, err
		}
	} else {
		srv.http = protocolConfig.Http
	}

	srv.setupHTTPRoutes()

	if protocolConfig.Http == nil {
		srv.http.Start()
	}

	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("failed creating seer client %v", err)
	}

	err = common.StartSeerBeacon(protocolConfig, sc, seerIface.ServiceTypePatrick)
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
