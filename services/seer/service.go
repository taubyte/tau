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
	seerIface "github.com/taubyte/tau/core/services/seer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	tauConfig "github.com/taubyte/tau/pkg/config"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/poe"
	servicesCommon "github.com/taubyte/tau/services/common"
)

var (
	logger = log.Logger("tau.seer.service")
)

func New(ctx context.Context, cfg tauConfig.Config, opts ...Options) (*Service, error) {
	var err error

	srv := &Service{
		config: cfg,
		shape:  cfg.Shape(),
	}

	poeFolder := os.DirFS(path.Join(cfg.Root(), "config", "poe", "star"))
	logger.Infof("poe folder: %s", poeFolder)
	if _, err := poeFolder.Open("dns.star"); err == nil {
		logger.Infof("creating poe engine")
		srv.poe, err = poe.New(poeFolder, "dns.star")
		if err != nil {
			return nil, fmt.Errorf("failed to create poe engine: %w", err)
		}
	}

	srv.dnsResolver = net.DefaultResolver
	srv.hostUrl = cfg.NetworkFqdn()

	for _, op := range opts {
		err = op(srv)
		if err != nil {
			return nil, err
		}
	}

	if srv.node = cfg.Node(); srv.node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, cfg, path.Join(cfg.Root(), servicesCommon.Seer))
		if err != nil {
			return nil, fmt.Errorf("new lite node failed with: %s", err)
		}
	}

	srv.devMode = cfg.DevMode()

	clientNode := srv.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}

	if srv.tns, err = tnsClient.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("new tns api failed with: %s", err)
	}
	if srv.ds, err = pebbleds.NewDatastore(
		path.Join(cfg.Root(), "storage", srv.shape, "seer"),
		nil,
	); err != nil {
		return nil, fmt.Errorf("initialize database failed with: %s", err)
	}
	srv.geo = &geoService{srv}
	srv.oracle = &oracleService{srv}
	if srv.stream, err = streams.New(srv.node, servicesCommon.Seer, servicesCommon.SeerProtocol); err != nil {
		return nil, fmt.Errorf("new p2p stream failed with: %w", err)
	}
	srv.setupStreamRoutes()
	srv.stream.Start()
	if err = srv.subscribe(); err != nil {
		return nil, fmt.Errorf("pubsub subscribe failed with: %s", err)
	}
	var sc seerIface.Client
	if sc, err = seerClient.New(ctx, clientNode, cfg.SensorsRegistry()); err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}
	if err = servicesCommon.StartSeerBeacon(cfg, sc, seerIface.ServiceTypeSeer, servicesCommon.SeerBeaconOptionMeta(map[string]string{"others": "dns"})); err != nil {
		return nil, fmt.Errorf("starting seer beacon failed with: %s", err)
	}
	ports := cfg.Ports()
	dnsPort := 0
	if ports != nil {
		dnsPort = ports["dns"]
	}
	if err = srv.newDnsServer(cfg.DevMode(), dnsPort); err != nil {
		logger.Error("creating Dns server failed with:", err.Error())
		return nil, fmt.Errorf("new dns server failed with: %s", err)
	}

	srv.dns.Start(ctx)

	// HTTP
	if srv.http = cfg.Http(); srv.http == nil {
		srv.http, err = auto.New(ctx, srv.node, cfg)
		if err != nil {
			return nil, fmt.Errorf("new http failed with: %s", err)
		}
		defer srv.http.Start()
	}

	srv.setupHTTPRoutes()

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
