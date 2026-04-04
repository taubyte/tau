package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/pkg/config"
	httpService "github.com/taubyte/tau/pkg/http"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/sensors"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	slices "github.com/taubyte/tau/utils/slices/string"
)

func Start(ctx context.Context, serviceConfig config.Config) error {
	setLogLevel()

	ctx, ctx_cancel := context.WithCancel(ctx)
	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		logger.Info("Exiting... Tau")
		ctx_cancel()
	}()

	storagePath := serviceConfig.Root() + "/storage/" + serviceConfig.Shape()

	err := createNodes(ctx, storagePath, serviceConfig.Shape(), serviceConfig)
	if err != nil {
		return err
	}

	// Start raft when the service list needs consensus (RequiresRaftCluster).
	if serviceConfig.Node() != nil && commonSpecs.RequiresRaftCluster(serviceConfig.Services()) {
		namespace := serviceConfig.Cluster()
		if namespace == "" {
			namespace = "main"
		}
		snapDir := raft.SnapshotDir(serviceConfig.Root(), serviceConfig.Shape(), namespace)
		raftOpts := []raft.Option{raft.WithSnapshotDir(snapDir)}
		if serviceConfig.DevMode() {
			raftOpts = append(raftOpts,
				raft.WithBootstrapTimeout(5*time.Second),
				raft.WithTimeouts(raft.TimeoutConfig{
					HeartbeatTimeout:   1 * time.Second,
					ElectionTimeout:    1 * time.Second,
					CommitTimeout:      500 * time.Millisecond,
					LeaderLeaseTimeout: 500 * time.Millisecond,
					SnapshotInterval:   2 * time.Minute,
					SnapshotThreshold:  8192,
				}),
			)
		}
		raftCluster, err := raft.New(serviceConfig.Node(), namespace, raftOpts...)
		if err != nil {
			return fmt.Errorf("creating raft cluster for namespace %q: %w", namespace, err)
		}
		serviceConfig.SetRaftCluster(raftCluster)
	}

	// start sensors service
	sensorsSvc, err := sensors.New(serviceConfig.Node())
	if err != nil {
		return fmt.Errorf("new sensors service failed with: %s", err)
	}
	serviceConfig.SetSensors(sensorsSvc)

	serviceConfig.SetDatabases(kvdb.New(serviceConfig.Node()))

	// Create httpNode if needed
	var httpNode httpService.Service
	for _, srv := range serviceConfig.Services() {
		if slices.Contains(commonSpecs.HTTPServices, srv) {
			httpNode, err = auto.New(ctx, serviceConfig.Node(), serviceConfig)
			if err != nil {
				return fmt.Errorf("new autoHttp failed with: %s", err)
			}

			serviceConfig.SetHttp(httpNode)
			break
		}
	}

	// Attach any p2p/http endpoints
	var includesNode bool
	services := make([]services.Service, 0)
	for _, srv := range serviceConfig.Services() {
		if srv == "substrate" {
			includesNode = true
			continue
		}

		srvPkg, ok := available[srv]
		if !ok {
			return fmt.Errorf("services `%s` does not exist ", srv)
		}

		_srv, err := srvPkg.New(ctx, serviceConfig)
		if err != nil {
			return fmt.Errorf("new for service `%s` failed with: %s", srv, err)
		}

		services = append(services, _srv)
	}

	// Running node last if included in list
	if includesNode {
		srvPkg, ok := available["substrate"]
		if !ok {
			return errors.New("node was not found in available packages")
		}

		_srv, err := srvPkg.New(ctx, serviceConfig)
		if err != nil {
			return fmt.Errorf("new for node failed with: %s", err)
		}

		services = append(services, _srv)
	}

	if httpNode != nil {
		httpNode.Start()
	}

	logger.Infof("%s started! with id: %s", serviceConfig.Shape(), serviceConfig.Node().ID())

	<-ctx.Done()
	for _, srv := range services {
		srv.Close()
	}

	if serviceConfig.RaftCluster() != nil {
		if err := serviceConfig.RaftCluster().Close(); err != nil {
			logger.Errorf("closing raft cluster: %v", err)
		}
	}

	if serviceConfig.ClientNode() != nil {
		serviceConfig.ClientNode().Close()
	}

	if serviceConfig.Databases() != nil {
		serviceConfig.Databases().Close()
	}

	if serviceConfig.Node() != nil {
		serviceConfig.Node().Close()
	}

	if serviceConfig.Http() != nil {
		serviceConfig.Http().Stop()
	}

	return nil
}
