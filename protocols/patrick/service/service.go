package service

import (
	"context"
	"fmt"
	"time"

	moody "bitbucket.org/taubyte/go-moody-blues"
	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	authAPI "github.com/taubyte/odo/clients/p2p/auth"
	monkeyApi "github.com/taubyte/odo/clients/p2p/monkey"
	seerClient "github.com/taubyte/odo/clients/p2p/seer"
	tnsApi "github.com/taubyte/odo/clients/p2p/tns"
	"github.com/taubyte/odo/config"
	odoConfig "github.com/taubyte/odo/config"
	auto "github.com/taubyte/odo/pkgs/http-auto"
	kv "github.com/taubyte/odo/pkgs/kvdb/database"
	protocolsCommon "github.com/taubyte/odo/protocols/common"

	streams "github.com/taubyte/p2p/streams/service"
)

var (
	BootstrapTime                   = 10 * time.Second
	logger, _                       = moody.New("patrick.service")
	DefaultReAnnounceJobTime        = 7 * time.Minute
	DefaultReAnnounceFailedJobsTime = 7 * time.Minute
)

func New(ctx context.Context, config *config.Protocol) (*PatrickService, error) {
	var srv PatrickService

	if config == nil {
		_cnf := &odoConfig.Protocol{}

		config = _cnf
	}

	err := config.Build(odoConfig.ConfigBuilder{
		DefaultP2PListenPort: protocolsCommon.PatrickDefaultP2PListenPort,
		DevHttpListenPort:    protocolsCommon.PatrickDevHttpListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	srv.devMode = config.DevMode

	logger.Error(moodyCommon.Object{"msg": config})

	if config.Node == nil {
		srv.node, err = odoConfig.NewNode(ctx, config, protocolsCommon.Patrick)
		if err != nil {
			return nil, err
		}
	} else {
		srv.node = config.Node
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
	srv.db, err = kv.New(logger.Std(), srv.node, protocolsCommon.Patrick, 5)
	if err != nil {
		return nil, fmt.Errorf("failed kv new with error: %w", err)
	}

	srv.stream, err = streams.New(srv.node, protocolsCommon.Patrick, protocolsCommon.PatrickProtocol)
	if err != nil {
		return nil, fmt.Errorf("failed stream new with error: %w", err)
	}

	srv.hostUrl = config.NetworkUrl
	srv.setupStreamRoutes()

	// HTTP
	if config.Http == nil {
		srv.http, err = auto.Configure(config).AutoHttp(srv.node)
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

	err = config.StartSeerBeacon(sc, seerIface.ServiceTypePatrick)
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
					logger.Error(moodyCommon.Object{"msg": err.Error()})
				}
			case <-time.After(DefaultReAnnounceFailedJobsTime):
				_ctx, cancel := context.WithTimeout(ctx, DefaultReAnnounceFailedJobsTime)
				err := srv.ReannounceFailedJobs(_ctx)
				cancel()
				if err != nil {
					logger.Error(moodyCommon.Object{"msg": err.Error()})
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return &srv, nil
}

func (srv *PatrickService) Close() error {
	// TODO use debug logger
	fmt.Println("Closing", protocolsCommon.Patrick)
	defer fmt.Println(protocolsCommon.Patrick, "closed")

	// node.ctx
	srv.stream.Stop()

	// ctx & partly relies on node
	srv.tnsClient.Close()
	srv.authClient.Close()
	srv.monkeyClient.Close()

	// ctx, needs to close after node as node will try to close it's store
	srv.db.Close()

	return nil
}
