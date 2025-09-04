//go:build !dev

package substrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/ipfs/go-log/v2"
	"github.com/shirou/gopsutil/cpu"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/kvdb"
	tbPlugins "github.com/taubyte/tau/pkg/vm-low-orbit"
	smartopsPlugins "github.com/taubyte/tau/pkg/vm-ops-orbit"
	orbit "github.com/taubyte/tau/pkg/vm-orbit/satellite/vm"
	seer "github.com/taubyte/tau/pkg/yaseer"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var (
	logger = log.Logger("tau.node.service")
)

// TODO: close on error
func New(ctx context.Context, config *tauConfig.Node) (*Service, error) {
	srv := &Service{
		ctx:      ctx,
		orbitals: make([]vm.Plugin, 0),
	}

	if config == nil {
		config = &tauConfig.Node{}
	}

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	srv.dev = config.DevMode
	srv.verbose = config.Verbose

	if config.Node == nil {
		if srv.node, err = tauConfig.NewLiteNode(ctx, config, path.Join(config.Root, protocolCommon.Substrate)); err != nil {
			return nil, fmt.Errorf("creating new lite node failed with: %w", err)
		}
	} else {
		srv.node = config.Node
	}

	srv.databases = config.Databases
	if srv.databases == nil {
		srv.databases = kvdb.New(srv.node)
	}

	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	beacon, err := srv.startBeacon(config)
	if err != nil {
		return nil, fmt.Errorf("starting beacon failed with: %w", err)
	}

	//TODO: This should not be needed
	if err = srv.startHttp(config); err != nil {
		return nil, fmt.Errorf("starting http service failed with %w", err)
	}

	if srv.tns, err = tnsClient.New(ctx, clientNode); err != nil {
		return nil, fmt.Errorf("creating tns client failed with %w", err)
	}

	if err = srv.startVm(); err != nil {
		return nil, fmt.Errorf("starting vm failed with %w", err)
	}

	if err = srv.attachNodes(config); err != nil {
		return nil, fmt.Errorf("attaching node services failed with: %w", err)
	}

	if err = tbPlugins.Initialize(ctx, srv.components.config()...); err != nil {
		return nil, fmt.Errorf("initializing Taubyte plugins failed with: %w", err)
	}

	if err = smartopsPlugins.Initialize(
		ctx,
		smartopsPlugins.SmartOpNode(srv.components.smartops),
	); err != nil {
		return nil, fmt.Errorf("initializing Taubyte smartops-plugins failed with: %w", err)
	}

	// Get/Load all plugins
	pluginDir := "/tb/plugins/"
	seer, err := seer.New(seer.SystemFS(pluginDir))
	if err != nil {
		if !config.DevMode {
			return nil, fmt.Errorf("creating systemFS seer for `%s` failed with %w", pluginDir, err)
		}
	} else {
		var plugConfig []string
		if _, err := os.Stat("/tb/plugins/plugins.yaml"); err == nil {
			if err = seer.Get("plugins").Document().Get(config.Shape).Value(&plugConfig); err != nil {
				return nil, fmt.Errorf("seer get plugins from shape `%s` failed with: %w", config.Shape, err)
			}

			for _, name := range plugConfig {
				pluginName := pluginDir + name
				plugin, err := orbit.Load(pluginName, ctx)
				if err != nil {
					return nil, fmt.Errorf("loading plugin `%s` failed with: %w", name, err)
				}

				srv.orbitals = append(srv.orbitals, plugin)
			}
		}
	}

	if config.Http == nil {
		srv.http.Start()
	}

	if len(config.P2PAnnounce) == 0 {
		logger.Error("P2P Announce is empty")
		return nil, errors.New("P2P Announce is empty")
	}

	if err = beacon.hostname(); err != nil {
		return nil, fmt.Errorf("setting beacon hostname failed with: %w", err)
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
