package seer

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"time"

	_ "embed"

	"github.com/ipfs/go-log/v2"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	commonSpec "github.com/taubyte/go-specs/common"
	streams "github.com/taubyte/p2p/streams/service"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	auto "github.com/taubyte/tau/pkgs/http-auto"
	"github.com/taubyte/tau/pkgs/kvdb"
	protocolsCommon "github.com/taubyte/tau/protocols/common"

	_ "modernc.org/sqlite"
)

var (
	logger = log.Logger("seer.service")
)

func New(ctx context.Context, config *tauConfig.Protocol, opts ...Options) (*Service, error) {
	if config == nil {
		config = &tauConfig.Protocol{}
	}

	srv := &Service{
		shape: config.Shape,
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	logger.Infof("Config: %#v", config)

	srv.dnsResolver = net.DefaultResolver
	srv.generatedDomain = config.GeneratedDomain
	srv.caaRecordBypass = regexp.MustCompile(fmt.Sprintf("tau.%s", config.NetworkUrl))

	for _, op := range opts {
		err = op(srv)
		if err != nil {
			return nil, err
		}
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, config, protocolsCommon.Seer)
		if err != nil {
			return nil, fmt.Errorf("new lite node failed with: %s", err)
		}
	} else {
		srv.node = config.Node
		srv.odo = true
	}

	srv.devMode = config.DevMode
	srv.dbFactory = config.Databases
	if srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	// For odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	srv.db, err = srv.dbFactory.New(logger, protocolsCommon.Seer, 5)
	if err != nil {
		return nil, fmt.Errorf("new key-value store failed with: %s", err)
	}

	// Setup/Start DNS service
	err = srv.newDnsServer(config.DevMode, config.Ports["dns"])
	if err != nil {
		logger.Error("creating Dns server failed with:", err.Error())
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
	srv.stream, err = streams.New(srv.node, protocolsCommon.Seer, commonSpec.SeerProtocol)
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

	err = protocolsCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeSeer, protocolsCommon.SeerBeaconOptionMeta(map[string]string{"others": "dns"}))
	if err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	// HTTP
	if config.Http == nil {
		srv.http, err = auto.NewAuto(ctx, srv.node, config)
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
	logger.Info("Closing", protocolsCommon.Seer)
	defer logger.Info()

	srv.stream.Stop()
	srv.tns.Close()
	srv.db.Close()
	srv.dns.Stop()

	srv.positiveCache.Stop()
	srv.negativeCache.Stop()
	return nil
}
