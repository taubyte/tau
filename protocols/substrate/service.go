//go:build !dev

package substrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/vm"
	"github.com/taubyte/go-seer"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/pkgs/kvdb"
	orbit "github.com/taubyte/vm-orbit/plugin/vm"

	protocolCommon "github.com/taubyte/tau/protocols/common"
	smartopsPlugins "github.com/taubyte/vm-core-plugins/smartops"
	tbPlugins "github.com/taubyte/vm-core-plugins/taubyte"
)

var (
	logger = log.Logger("node.service")
)

func New(ctx context.Context, config *tauConfig.Protocol) (*Service, error) {
	srv := &Service{
		ctx:      ctx,
		orbitals: make([]vm.Plugin, 0),
	}

	if config == nil {
		config = &tauConfig.Protocol{}
	}

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	srv.dev = config.DevMode
	srv.verbose = config.Verbose

	if config.Node == nil {
		srv.node, err = tauConfig.NewLiteNode(ctx, config, protocolCommon.Substrate)
		if err != nil {
			return nil, fmt.Errorf("creating new lite node failed with: %w", err)
		}
	} else {
		srv.node = config.Node
	}

	srv.databases = config.Databases
	if srv.databases == nil {
		srv.databases = kvdb.New(srv.node)
	}

	// For Odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	beacon, err := srv.startBeacon(config)
	if err != nil {
		return nil, fmt.Errorf("starting beacon failed with: %w", err)
	}

	// HTTP
	err = srv.startHttp(config)
	if err != nil {
		return nil, fmt.Errorf("starting http service failed with %w", err)
	}

	srv.tns, err = tnsClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating tns client failed with %w", err)
	}

	if err = srv.startVm(); err != nil {
		return nil, fmt.Errorf("starting vm failed with %w", err)
	}

	if err = srv.attachNodes(config); err != nil {
		return nil, fmt.Errorf("attaching node services failed with: %w", err)
	}

	if err = tbPlugins.Initialize(ctx,
		tbPlugins.PubsubNode(srv.nodePubSub),
		tbPlugins.IpfsNode(srv.nodeIpfs),
		tbPlugins.DatabaseNode(srv.nodeDatabase),
		tbPlugins.StorageNode(srv.nodeStorage),
		tbPlugins.P2PNode(srv.nodeP2P),
	); err != nil {
		return nil, fmt.Errorf("initializing Taubyte plugins failed with: %w", err)
	}

	if err = smartopsPlugins.Initialize(
		ctx,
		smartopsPlugins.SmartOpNode(srv.nodeSmartOps),
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

	return srv, nil
}

func (srv *Service) Orbitals() []vm.Plugin {
	return srv.orbitals
}

func (srv *Service) Close() error {
	logger.Info("Closing", protocolCommon.Substrate)
	defer logger.Info(protocolCommon.Substrate, "closed")

	for _, orbitals := range srv.orbitals {
		if err := orbitals.Close(); err != nil {
			logger.Errorf("Failed to close orbital `%s`", orbitals.Name())
		}
	}

	srv.tns.Close()

	srv.nodeHttp.Close()
	srv.nodePubSub.Close()
	srv.nodeIpfs.Close()
	srv.nodeDatabase.Close()
	srv.nodeStorage.Close()
	srv.nodeP2P.Close()
	srv.nodeCounters.Close()
	srv.nodeSmartOps.Close()

	srv.vm.Close()

	return nil
}

func (srv *Service) Dev() bool {
	return srv.dev
}
