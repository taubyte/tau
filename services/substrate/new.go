package substrate

import (
	"context"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/shirou/gopsutil/v4/cpu"
	hoarderClient "github.com/taubyte/tau/clients/p2p/hoarder"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	"github.com/taubyte/tau/core/vm"
	tauConfig "github.com/taubyte/tau/pkg/config"
	tbPlugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	protocolCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/substrate/migration"
)

var (
	logger = log.Logger("tau.node.service")
)

// TODO: close on error
func New(ctx context.Context, cfg tauConfig.Config) (*Service, error) {
	srv := &Service{
		ctx:      ctx,
		orbitals: make([]vm.Plugin, 0),
	}

	var err error
	srv.dev = cfg.DevMode()
	srv.verbose = cfg.Verbose()
	srv.cluster = cfg.Cluster()

	if srv.node = cfg.Node(); srv.node == nil {
		if srv.node, err = tauConfig.NewLiteNode(ctx, cfg, path.Join(cfg.Root(), protocolCommon.Substrate)); err != nil {
			return nil, fmt.Errorf("creating new lite node failed with: %w", err)
		}
	}

	clientNode := srv.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}

	//TODO: This should not be needed
	if err = srv.startHttp(cfg); err != nil {
		return nil, fmt.Errorf("starting http service failed with %w", err)
	}

	if srv.tns, err = tnsClient.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("creating tns client failed with %w", err)
	}

	if srv.hoarderClient, err = hoarderClient.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("creating hoarder client failed with %w", err)
	}

	// Move any node-local project data (an upgraded node's databases, storage
	// metadata and file bytes) to the hoarders before serving: bounded
	// synchronous pass, background continuation past the bound. Fresh nodes
	// pay one empty prefix query.
	srv.migrator = migration.New(ctx, srv.node, srv.hoarderClient, srv.tns)
	srv.migrator.Boot()

	if err = srv.startVm(); err != nil {
		return nil, fmt.Errorf("starting vm failed with %w", err)
	}

	if err = srv.attachNodes(cfg); err != nil {
		return nil, fmt.Errorf("attaching node services failed with: %w", err)
	}

	if err = tbPlugins.Initialize(ctx, srv.components.config()...); err != nil {
		return nil, fmt.Errorf("initializing Taubyte plugins failed with: %w", err)
	}

	if cfg.Http() == nil {
		srv.http.Start()
	}

	if len(cfg.P2PAnnounce()) == 0 {
		logger.Error("P2P Announce is empty")
		return nil, errors.New("P2P Announce is empty")
	}

	if err = srv.startStream(); err != nil {
		return nil, fmt.Errorf("starting p2p stream failed with: %w", err)
	}

	if err = srv.startCheckCpu(); err != nil {
		return nil, fmt.Errorf("starting cpu check failed with: %w", err)
	}

	return srv, nil
}

var (
	CPUCheckInterval = time.Second
)

func (s *Service) startCheckCpu() error {
	// First run to check if call is successful
	cpuUsage, err := cpu.Percent(0, true)
	if err != nil {
		return err
	}

	// cache the cpu count, this shouldnt change
	s.cpuCount = len(cpuUsage)
	var cpuSum float64
	// manually calculate  average, skips 1 extra call of cpu.Percent
	for _, usage := range cpuUsage {
		cpuSum += usage
	}
	s.cpuAverage = cpuSum / float64(s.cpuCount)

	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
				// setting perCpu param to false returns a single average
				// setting interval to greater than 0 will sleep for given interval duration
				cpuUsage, err := cpu.Percent(CPUCheckInterval, false)
				if err != nil {
					logger.Errorf("checking cpu usage failed with: %w", err)
				}

				s.cpuAverage = cpuUsage[0]
			}
		}
	}()

	return nil
}
