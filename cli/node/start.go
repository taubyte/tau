package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	logging "github.com/ipfs/go-log/v2"
	ifaceCommon "github.com/taubyte/go-interfaces/common"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	commonSpecs "github.com/taubyte/go-specs/common"
	auto "github.com/taubyte/odo/pkgs/http-auto"
	"github.com/taubyte/p2p/peer"
	slices "github.com/taubyte/utils/slices/string"

	httpService "github.com/taubyte/http"
)

func Start(ctx context.Context, config *commonIface.GenericConfig, shape string) error {
	lvl, _ := logging.LevelFromString("FATAL")
	logging.SetAllLoggers(lvl)

	config.Shape = shape

	ctx, ctx_cancel := context.WithCancel(ctx)
	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		fmt.Println("Exiting... Odo")
		ctx_cancel()
	}()

	dbPath := commonIface.DatabasePath + shape

	if config.DevMode {
		dbPath = shape
	}

	config.Verbose = true
	setNetworkDomains(config)

	var err error
	if len(config.Protocols) < 1 {
		peerInfo, err := commonIface.ConvertToAddrInfo(config.Peers)
		if err != nil {
			return err
		}

		config.Node, err = peer.NewFull(ctx, dbPath, config.PrivateKey, config.SwarmKey, config.P2PListen, config.P2PAnnounce, true, peer.BootstrapParams{Enable: true, Peers: peerInfo})
		if err != nil {
			return fmt.Errorf("creating new full node failed with: %s", err)
		}
	} else {
		config.Node, err = NewNode(ctx, config, dbPath)
		if err != nil {
			return fmt.Errorf("creating new node for shape `%s` failed with: %s", shape, err)
		}

		// Create client node
		config.ClientNode, err = createClientNode(ctx, config, shape, dbPath)
		if err != nil {
			return fmt.Errorf("creating client node failed with: %s", err)
		}
	}

	// Create httpNode if needed
	var httpNode httpService.Service
	for _, srv := range config.Protocols {
		if slices.Contains(commonSpecs.HttpProtocols, srv) {
			httpNode, err = auto.Configure(config).AutoHttp(config.Node)
			if err != nil {
				return fmt.Errorf("new autoHttp failed with: %s", err)
			}

			config.Http = httpNode
			break
		}
	}

	// Attach any p2p/http endpoints
	var includesNode bool
	services := make([]ifaceCommon.Service, 0)
	for _, srv := range config.Protocols {
		if srv == "node" {
			includesNode = true
			continue
		}

		srvPkg, ok := available[srv]
		if !ok {
			return fmt.Errorf("services `%s` does not exist ", srv)
		}

		_srv, err := srvPkg.New(ctx, config)
		if err != nil {
			return fmt.Errorf("new for service `%s` failed with: %s", srv, err)
		}

		services = append(services, _srv)
	}

	// Running node last if included in list
	if includesNode {
		srvPkg, ok := available["node"]
		if !ok {
			return errors.New("node was not found in available packages")
		}

		_srv, err := srvPkg.New(ctx, config)
		if err != nil {
			return fmt.Errorf("new for node failed with: %s", err)
		}

		services = append(services, _srv)
	}

	if httpNode != nil {
		httpNode.Start()
	}

	fmt.Printf("\n CONFIG DUMP FOR SHAPE %s: %#v\n", shape, config)
	fmt.Printf("%s started! with id: %s\n", shape, config.Node.ID())

	// https://github.com/ipfs/go-ipfs/blob/8f623c9124d6c0b1d511a072a4d13633884c7b40/core/builder.go

	<-ctx.Done()
	for _, srv := range services {
		srv.Close()
	}

	fmt.Println("Waiting for Protocols to shutdown...")
	time.Sleep(10 * time.Second)

	if config.ClientNode != nil {
		config.ClientNode.Close()
	}

	if config.Node != nil {
		config.Node.Close()
	}

	if config.Http != nil {
		config.Http.Stop()
	}

	fmt.Println("Waiting for Nodes to shutdown...")
	time.Sleep(5 * time.Second)

	return nil
}
