package service

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	moody "bitbucket.org/taubyte/go-moody-blues"
	auto "bitbucket.org/taubyte/http-auto"
	kv "bitbucket.org/taubyte/kvdb/database"
	configutils "bitbucket.org/taubyte/p2p/config"
	streams "bitbucket.org/taubyte/p2p/streams/service"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	commonSpec "github.com/taubyte/go-specs/common"
	seerClient "github.com/taubyte/odo/clients/p2p/seer"
	tnsClient "github.com/taubyte/odo/clients/p2p/tns"

	_ "embed"

	p2pDatastore "bitbucket.org/taubyte/p2p/peer"

	_ "modernc.org/sqlite"

	common "github.com/taubyte/odo/protocols/seer/common"
)

var (
	logger, _ = moody.New("seer.service")
)

func New(ctx context.Context, config *commonIface.GenericConfig, opts ...Options) (*Service, error) {
	srv := &Service{}

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
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	if !config.DevMode {
		p2pDatastore.Datastore = "pebble"
	}

	logger.Info(moodyCommon.Object{"message": fmt.Sprintf("Config: %#v", config)})

	srv.dnsResolver = net.DefaultResolver
	srv.generatedDomain = config.Domains.Generated
	srv.caaRecordBypass = regexp.MustCompile(fmt.Sprintf("tau.%s", config.NetworkUrl))

	for _, op := range opts {
		err = op(srv)
		if err != nil {
			return nil, err
		}
	}

	if config.Node == nil {
		srv.node, err = configutils.NewLiteNode(ctx, config, common.DatabaseName)
		if err != nil {
			return nil, fmt.Errorf("new lite node failed with: %s", err)
		}
	} else {
		srv.node = config.Node
		srv.odo = true
	}

	// For odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	srv.db, err = kv.New(logger.Std(), srv.node, common.DatabaseName, 5)
	if err != nil {
		return nil, fmt.Errorf("new key-value store failed with: %s", err)
	}

	// Setup/Start DNS service
	err = srv.newDnsServer(config.DevMode, config.DnsPort)
	if err != nil {
		logger.Error(moodyCommon.Object{"message": fmt.Sprintf("creating Dns server failed with: %s", err)})
		return nil, fmt.Errorf("new dns server failed with: %s", err)
	}

	srv.tns, err = tnsClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("new tns api failed with: %s", err)
	}

	// will panic if fails
	srv.dns.Start(ctx)
	err = srv.subscribe()
	if err != nil {
		return nil, fmt.Errorf("pubsub subscribe failed with: %s", err)
	}

	// Setup geo and oracle
	srv.geo = &geoService{seer: srv}

	srv.oracle = &oracleService{seer: srv}

	err = initializeDB(srv, config)
	if err != nil {
		return nil, fmt.Errorf("initialize database failed with: %s", err)
	}

	// Stream
	srv.stream, err = streams.New(srv.node, common.ServiceName, commonSpec.SeerProtocol)
	if err != nil {
		return nil, fmt.Errorf("new p2p stream failed with: %w", err)
	}

	srv.hostUrl = config.NetworkUrl
	srv.setupStreamRoutes()

	// Beacon
	if config.DevMode {
		seerClient.DefaultAnnounceBeaconInterval = 30 * time.Second // To help with testing dns
	}

	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = config.StartSeerBeacon(sc, seerIface.ServiceTypeSeer, commonIface.SeerBeaconOptionMeta(map[string]string{"others": "dns"}))
	if err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	// HTTP
	if config.Http == nil {
		srv.http, err = auto.Configure(config).AutoHttp(srv.node)
		if err != nil {
			return nil, fmt.Errorf("new http failed with: %s", err)
		}
	} else {
		srv.http = config.Http
	}

	srv.setupHTTPRoutes()

	if config.Http == nil {
		srv.http.Start()
	}

	return srv, nil
}

func (srv *Service) Close() error {
	// TODO use debug logger
	fmt.Println("Closing", common.DatabaseName)
	defer fmt.Println(common.DatabaseName, "closed")

	// node.ctx
	srv.stream.Stop()

	// ctx & partly relies on node
	srv.tns.Close()

	srv.db.Close()

	// ctx
	if !srv.odo {
		srv.node.Close()
		srv.http.Stop()
	}

	// ctx, needs to close after node as node will try to close it's store

	// ctx
	srv.dns.Stop()
	return nil
}
