package service

import (
	"context"
	"fmt"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	moody "bitbucket.org/taubyte/go-moody-blues"
	auto "bitbucket.org/taubyte/http-auto"
	kv "bitbucket.org/taubyte/kvdb/database"
	"bitbucket.org/taubyte/p2p/peer"
	streams "bitbucket.org/taubyte/p2p/streams/service"
	options "github.com/taubyte/http/options"
	common "github.com/taubyte/odo/protocols/billing/common"

	authClient "bitbucket.org/taubyte/auth/api/p2p"
	configutils "bitbucket.org/taubyte/p2p/config"
	seerClient "bitbucket.org/taubyte/seer-p2p-client"
	"github.com/taubyte/go-interfaces/services/billing"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
)

var (
	logger, _ = moody.New("billing.service")
)

func New(ctx context.Context, config *commonIface.GenericConfig) (billing.Service, error) {
	var srv BillingService

	if config == nil {
		_cnf := &commonIface.GenericConfig{}
		_cnf.Bootstrap = true

		config = _cnf
	}

	err := config.Build(commonIface.ConfigBuilder{
		DefaultP2PListenPort: common.DefaultP2PListenPort,
		DevHttpListenPort:    common.DevHttpListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, err
	}

	if config.DevMode == false {
		peer.Datastore = "pebble"
	}

	srv.ctx = ctx

	srv.node, err = configutils.NewLiteNode(ctx, config, common.DatabaseName)
	if err != nil {
		return nil, err
	}

	srv.db, err = kv.New(logger.Std(), srv.node, common.DatabaseName, 5)
	if err != nil {
		return nil, err
	}

	srv.customers = &customersService{billing: &srv}

	srv.stream, err = streams.New(srv.node, common.ServiceName, common.Protocol)
	if err != nil {
		return nil, err
	}

	srv.setupStreamRoutes()

	sc, err := seerClient.New(ctx, srv.node)
	if err != nil {
		return nil, fmt.Errorf("Creating seer client failed with %s", err)
	}

	err = config.StartSeerBeacon(sc, seerIface.ServiceTypeBilling)
	if err != nil {
		return nil, err
	}

	srv.authClient, err = authClient.New(ctx, srv.node)
	if err != nil {
		return nil, err
	}

	srv.http, err = auto.Configure(config).AutoHttp(srv.node, options.AllowedOrigins(false, nil))
	if err != nil {
		return nil, err
	}

	srv.setupHTTPRoutes()
	srv.http.Start()

	if config.DevMode {
		srv.sandbox = true
	}

	return &srv, nil
}

func (srv *BillingService) Close() error {
	// TODO use debug logger
	fmt.Println("Closing", common.DatabaseName)
	defer fmt.Println(common.DatabaseName, "closed")

	// node.ctx
	srv.stream.Stop()

	// ctx & partly relies on node
	srv.authClient.Close()

	// ctx, needs to close after node as node will try to close it's store
	srv.db.Close()

	// ctx
	srv.node.Close()

	// ctx
	srv.http.Stop()
	return nil
}
