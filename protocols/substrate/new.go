//go:build !dev

package substrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/go-interfaces/services/substrate/components"
	"github.com/taubyte/go-interfaces/vm"
	"github.com/taubyte/go-seer"
	"github.com/taubyte/tau/clients/p2p/substrate"
	tnsClient "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	"github.com/taubyte/tau/pkgs/kvdb"
	"github.com/taubyte/tau/vm/helpers"
	"github.com/taubyte/utils/maps"
	orbit "github.com/taubyte/vm-orbit/plugin/vm"

	con "github.com/taubyte/p2p/streams"
	"github.com/taubyte/p2p/streams/command"
	"github.com/taubyte/p2p/streams/command/response"
	streams "github.com/taubyte/p2p/streams/service"
	httptun "github.com/taubyte/p2p/streams/tunnels/http"
	protocolCommon "github.com/taubyte/tau/protocols/common"
	http "github.com/taubyte/tau/protocols/substrate/components/http/common"
	smartopsPlugins "github.com/taubyte/vm-core-plugins/smartops"
	tbPlugins "github.com/taubyte/vm-core-plugins/taubyte"
)

var (
	logger = log.Logger("node.service")
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

	return srv, nil
}

func (s *Service) startStream() (err error) {
	if s.stream, err = streams.New(s.node, protocolCommon.Substrate, protocolCommon.SubstrateProtocol); err != nil {
		return fmt.Errorf("new stream failed with: %w", err)
	}

	if err := s.stream.DefineStream(substrate.Command, s.proxyHandler, s.proxyTunnel); err != nil {
		return fmt.Errorf("defining command `%s` failed with: %w", substrate.Command, err)
	}

	return
}

func (s *Service) proxyHandler(ctx context.Context, con con.Connection, body command.Body) (response.Response, error) {
	proxyType, err := maps.String(body, substrate.BodyType)
	if err != nil {
		return nil, err
	}

	switch proxyType {
	case substrate.ProxyHTTP:
		return s.proxyHttp(body)
	default:
		return nil, fmt.Errorf("proxy type `%s` not supported", proxyType)
	}
}

func (s *Service) proxyTunnel(ctx context.Context, rw io.ReadWriter) {
	w, r, err := httptun.Backend(rw)
	if err != nil {
		fmt.Fprintf(rw, "Status: %d\nerror: %s", 500, err.Error())
		return
	}

	s.nodeHttp.Handler(w, r)
}

func (s *Service) proxyHttp(body command.Body) (response.Response, error) {
	host, err := maps.String(body, substrate.BodyHost)
	if err != nil {
		return nil, err
	}

	path, err := maps.String(body, substrate.BodyPath)
	if err != nil {
		return nil, err
	}

	method, err := maps.String(body, substrate.BodyMethod)
	if err != nil {
		return nil, err
	}

	response := make(map[string]interface{})
	response[substrate.ResponseCached] = false

	matcher := http.New(helpers.ExtractHost(host), path, method)
	servs, err := s.nodeHttp.Cache().Get(matcher, components.GetOptions{Validation: true})
	if err == nil && len(servs) > 0 {
		response[substrate.ResponseCached] = true
	}

	return response, nil
}
