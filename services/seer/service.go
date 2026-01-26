package seer

import (
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"time"

	_ "embed"

	pebbleds "github.com/ipfs/go-ds-pebble"
	"github.com/ipfs/go-log/v2"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	seerIface "github.com/taubyte/tau/core/services/seer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/poe"
	servicesCommon "github.com/taubyte/tau/services/common"
)

var (
	logger = log.Logger("tau.seer.service")
)

func New(ctx context.Context, config *tauConfig.Node, opts ...Options) (*Service, error) {
	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, fmt.Errorf("building config failed with: %s", err)
	}

	srv := &Service{
		config: config,
		shape:  config.Shape,
	}

	poeFolder := os.DirFS(path.Join(config.Root, "config", "poe", "star"))
	logger.Infof("poe folder: %s", poeFolder)
	if _, err := poeFolder.Open("dns.star"); err == nil {
		logger.Infof("creating poe engine")
		srv.poe, err = poe.New(poeFolder, "dns.star")
		if err != nil {
			return nil, fmt.Errorf("failed to create poe engine: %w", err)
		}
	}

	srv.dnsResolver = net.DefaultResolver
	srv.hostUrl = config.NetworkFqdn

	for _, op := range opts {
		err = op(srv)
		if err != nil {
			return nil, err
		}
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, config, path.Join(config.Root, servicesCommon.Seer))
		if err != nil {
			return nil, fmt.Errorf("new lite node failed with: %s", err)
		}
	} else {
		srv.node = config.Node
	}

	srv.devMode = config.DevMode

	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	srv.tns, err = tnsClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("new tns api failed with: %s", err)
	}

	srv.ds, err = pebbleds.NewDatastore(
		path.Join(config.Root, "storage", srv.shape, "seer"),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize database failed with: %s", err)
	}

	srv.geo = &geoService{srv}
	srv.oracle = &oracleService{srv}

	srv.stream, err = streams.New(srv.node, servicesCommon.Seer, servicesCommon.SeerProtocol)
	if err != nil {
		return nil, fmt.Errorf("new p2p stream failed with: %w", err)
	}

	srv.setupStreamRoutes()
	srv.stream.Start()

	err = srv.subscribe()
	if err != nil {
		return nil, fmt.Errorf("pubsub subscribe failed with: %s", err)
	}

	sc, err := seerClient.New(ctx, clientNode, config.SensorsRegistry())
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = servicesCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeSeer, servicesCommon.SeerBeaconOptionMeta(map[string]string{"others": "dns"}))
	if err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}

	// Start DNS
	err = srv.newDnsServer(config.DevMode, config.Ports["dns"])
	if err != nil {
		logger.Error("creating Dns server failed with:", err.Error())
		return nil, fmt.Errorf("new dns server failed with: %s", err)
	}

	srv.dns.Start(ctx)

	// HTTP
	if config.Http == nil {
		srv.http, err = auto.New(ctx, srv.node, config)
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
	logger.Info("Closing", servicesCommon.Seer)
	defer logger.Info()

	srv.stream.Stop()

	time.Sleep(100 * time.Millisecond)

	srv.tns.Close()
	srv.ds.Close()

	srv.dns.Stop()

	srv.positiveCache.Stop()
	srv.negativeCache.Stop()
	return nil
}
