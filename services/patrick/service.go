package service

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/ipfs/go-log/v2"
	authAPI "github.com/taubyte/tau/clients/p2p/auth"
	monkeyApi "github.com/taubyte/tau/clients/p2p/monkey"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	seerIface "github.com/taubyte/tau/core/services/seer"
	tauConfig "github.com/taubyte/tau/pkg/config"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	servicesCommon "github.com/taubyte/tau/services/common"

	kvdbIface "github.com/taubyte/tau/core/kvdb"

	"github.com/taubyte/tau/p2p/peer"
	streamClient "github.com/taubyte/tau/p2p/streams/client"
	streams "github.com/taubyte/tau/p2p/streams/service"
)

var (
	BootstrapTime            = 10 * time.Second
	logger                   = log.Logger("tau.patrick.service")
	DefaultReAnnounceJobTime = 1 * time.Minute
	MaxReAnnounceJobs        = 10
)

func New(ctx context.Context, cfg tauConfig.Config) (*PatrickService, error) {
	if cfg == nil {
		return nil, fmt.Errorf("building config failed with: you must define p2p port")
	}
	var srv PatrickService

	srv.ctx, srv.cancel = context.WithCancel(ctx)

	var err error
	srv.devMode = cfg.DevMode()
	srv.cluster = cfg.Cluster()
	if cfg.RaftCluster() == nil {
		return nil, fmt.Errorf("raft cluster is required")
	}
	srv.raftCluster = cfg.RaftCluster()
	srv.jobQueue = raft.NewQueue(cfg.RaftCluster(), "patrick")

	if srv.node = cfg.Node(); srv.node == nil {
		srv.node, err = tauConfig.NewNode(srv.ctx, cfg, path.Join(cfg.Root(), servicesCommon.Patrick))
		if err != nil {
			return nil, err
		}
	}

	if srv.dbFactory = cfg.Databases(); srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	clientNode := srv.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}

	// Auth Consumer/Client
	if srv.authClient, err = authAPI.New(srv.ctx, clientNode); err != nil {
		return nil, fmt.Errorf("failed auth api new with error: %w", err)
	}
	if srv.tnsClient, err = tnsApi.New(ctx, clientNode); err != nil {
		return nil, err
	}
	if srv.monkeyClient, err = monkeyApi.New(srv.ctx, clientNode); err != nil {
		return nil, err
	}
	if srv.db, err = srv.dbFactory.New(logger, servicesCommon.Patrick, 5); err != nil {
		return nil, fmt.Errorf("failed kv new with error: %w", err)
	}
	if srv.outboundClient, err = streamClient.New(srv.node, servicesCommon.PatrickProtocol); err != nil {
		return nil, fmt.Errorf("creating outbound patrick client: %w", err)
	}
	go srv.runClusterHeartbeat()
	if srv.stream, err = streams.New(srv.node, servicesCommon.Patrick, servicesCommon.PatrickProtocol); err != nil {
		return nil, fmt.Errorf("failed stream new with error: %w", err)
	}

	srv.hostUrl = cfg.NetworkFqdn()
	srv.setupStreamRoutes()
	srv.stream.Start()

	// HTTP
	if srv.http = cfg.Http(); srv.http == nil {
		if srv.http, err = auto.New(srv.ctx, srv.node, cfg); err != nil {
			return nil, err
		}
		defer srv.http.Start()
	}

	srv.setupHTTPRoutes()

	var sc seerIface.Client
	if sc, err = seerClient.New(srv.ctx, clientNode, cfg.SensorsRegistry()); err != nil {
		return nil, fmt.Errorf("failed creating seer client %v", err)
	}
	if err = servicesCommon.StartSeerBeacon(cfg, sc, seerIface.ServiceTypePatrick); err != nil {
		return nil, err
	}

	// Go routine to re announce any pending jobs (queue-based or pubsub-based)
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

const clusterHeartbeatInterval = 30 * time.Second

func (srv *PatrickService) runClusterHeartbeat() {
	ticker := time.NewTicker(clusterHeartbeatInterval)
	defer ticker.Stop()
	for {
		if err := srv.writeClusterHeartbeat(srv.ctx); err != nil {
			logger.Errorf("cluster heartbeat failed: %v", err)
		}
		select {
		case <-srv.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (srv *PatrickService) writeClusterHeartbeat(ctx context.Context) error {
	ts := time.Now().Unix()
	pid := srv.node.ID().String()
	key := "/cluster/" + srv.cluster
	pidKey := key + "/pid"
	// heartbeat value: timestamp
	val := []byte(fmt.Sprintf("%d", ts))
	if err := srv.db.Put(ctx, key, val); err != nil {
		return err
	}
	pidVal, err := cbor.Marshal(map[string]interface{}{"pid": pid, "timestamp": ts})
	if err != nil {
		return err
	}
	return srv.db.Put(ctx, pidKey, pidVal)
}

func (srv *PatrickService) Close() error {
	logger.Info("Closing", servicesCommon.Patrick)
	defer logger.Info(servicesCommon.Patrick, "closed")

	srv.cancel()
	srv.jobQueue.Close()
	if srv.outboundClient != nil {
		srv.outboundClient.Close()
	}

	srv.stream.Stop()
	srv.db.Close()

	return nil
}
