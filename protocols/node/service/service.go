//go:build !dev

package service

import (
	"context"
	"errors"
	"fmt"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	moody "bitbucket.org/taubyte/go-moody-blues"
	configutils "bitbucket.org/taubyte/p2p/config"
	peer "bitbucket.org/taubyte/p2p/peer"
	tnsClient "bitbucket.org/taubyte/tns-p2p-client"
	moodyCommon "github.com/taubyte/go-interfaces/moody"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	nodeP2PIFace "github.com/taubyte/go-interfaces/services/substrate/p2p"
	"github.com/taubyte/go-interfaces/vm"
	"github.com/taubyte/go-seer"
	"github.com/taubyte/odo/protocols/node/common"
	orbit "github.com/taubyte/vm-orbit/plugin/vm"
	smartopsPlugins "github.com/taubyte/vm-plugins/smartops"
	tbPlugins "github.com/taubyte/vm-plugins/taubyte"
)

var (
	logger, _ = moody.New("node.service")
)

func New(ctx context.Context, config *commonIface.GenericConfig) (*Service, error) {
	srv := &Service{
		ctx:      ctx,
		orbitals: make([]vm.Plugin, 0),
	}

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
		return nil, err
	}
	srv.branch = config.Branch

	if !config.DevMode {
		peer.Datastore = "pebble"
	}

	if config.Node == nil {
		srv.node, err = configutils.NewLiteNode(ctx, config, common.DatabaseName)
		if err != nil {
			return nil, fmt.Errorf("creating new lite node failed with: %w", err)
		}
	} else {
		srv.node = config.Node
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
		logger.Error(moodyCommon.Object{"message": errors.New("P2P Announce is empty")})
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
	// TODO use debug logger
	fmt.Println("Closing", common.DatabaseName)
	defer fmt.Println(common.DatabaseName, "closed")

	for _, orbitals := range srv.orbitals {
		if err := orbitals.Close(); err != nil {
			fmt.Printf("Failed to close orbital `%s`\n", orbitals.Name())
		}
	}

	// ctx & partly relies on node
	srv.tns.Close()

	srv.nodeHttp.Close()
	srv.nodePubSub.Close()
	srv.nodeIpfs.Close()
	srv.nodeDatabase.Close()
	srv.nodeStorage.Close()
	srv.nodeP2P.Close()
	srv.nodeCounters.Close()
	srv.nodeSmartOps.Close()

	// ctx
	srv.node.Close()

	// ctx
	srv.vm.Close()

	// ctx
	srv.http.Stop()
	return nil
}

func (srv *Service) P2PNode() (nodeP2P nodeP2PIFace.Service, err error) {
	if srv.nodeP2P == nil {
		return nil, errors.New("nodeP2P not created")
	}

	return srv.nodeP2P, nil
}
