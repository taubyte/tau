package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	moody "bitbucket.org/taubyte/go-moody-blues/common"
	ifaceCommon "github.com/taubyte/go-interfaces/common"
	commonIface "github.com/taubyte/go-interfaces/services/common"
	httpService "github.com/taubyte/go-interfaces/services/http"
	commonSpecs "github.com/taubyte/go-specs/common"
	"github.com/taubyte/odo/config"
	auto "github.com/taubyte/odo/pkgs/http-auto"
	slices "github.com/taubyte/utils/slices/string"
)

func Start(ctx context.Context, config *config.Protocol) error {
	moody.LogLevel(moody.DebugLevelFatal)

	ctx, ctx_cancel := context.WithCancel(ctx)
	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		fmt.Println("Exiting... Odo")
		ctx_cancel()
	}()

	databasePath := commonIface.DatabasePath + config.Shape

	if config.DevMode {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		config.Root = dir
		databasePath = config.Shape
	}
	config.Verbose = true

	err := createP2PNodes(ctx, databasePath, config.Shape, config)
	if err != nil {
		return err
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

	// TODO: Use logger
	fmt.Printf("%s started! with id: %s\n", config.Shape, config.Node.ID())

	// https://github.com/ipfs/go-ipfs/blob/8f623c9124d6c0b1d511a072a4d13633884c7b40/core/builder.go

	<-ctx.Done()
	for _, srv := range services {
		srv.Close()
	}

	if config.ClientNode != nil {
		config.ClientNode.Close()
	}

	if config.Node != nil {
		config.Node.Close()
	}

	if config.Http != nil {
		config.Http.Stop()
	}

	return nil
}
