package seer

import (
	"context"
	"fmt"
	"net"
	"path"
	"time"

	_ "embed"

	pebbleds "github.com/ipfs/go-ds-pebble"
	"github.com/ipfs/go-log/v2"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	commonSpec "github.com/taubyte/go-specs/common"
	streams "github.com/taubyte/p2p/streams/service"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	auto "github.com/taubyte/tau/pkgs/http-auto"
	protocolsCommon "github.com/taubyte/tau/protocols/common"

)

var (
	logger = log.Logger("tau.seer.service")
)

func New(ctx context.Context, config *tauConfig.Node, opts ...Options) (*Service, error) {
	if config == nil {
		config = &tauConfig.Node{}
	}

	srv := &Service{
		config: config,
		shape:  config.Shape,
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	srv.dnsResolver = net.DefaultResolver

	for _, op := range opts {
		err = op(srv)
		if err != nil {
			return nil, err
		}
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, config, path.Join(config.Root, protocolsCommon.Seer))
		if err != nil {
			return nil, fmt.Errorf("new lite node failed with: %s", err)
		}
	} else {
		srv.node = config.Node
		srv.odo = true
	}

	srv.devMode = config.DevMode

	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
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

	srv.ds, err = pebbleds.NewDatastore(
		path.Join(config.Root, "storage", srv.shape, "seer"),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize database failed with: %s", err)
	}

	// Stream
	srv.stream, err = streams.New(srv.node, protocolsCommon.Seer, commonSpec.SeerProtocol)
	if err != nil {
		return nil, fmt.Errorf("new p2p stream failed with: %w", err)
	}

	srv.hostUrl = config.NetworkFqdn
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

	srv.ds.Close()

	srv.dns.Stop()

	srv.positiveCache.Stop()
	srv.negativeCache.Stop()
	return nil
}
