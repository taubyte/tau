package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ipfs/go-log/v2"

	"github.com/taubyte/go-interfaces/services"
	commonSpecs "github.com/taubyte/go-specs/common"
	httpService "github.com/taubyte/http"
	"github.com/taubyte/odo/config"
	auto "github.com/taubyte/odo/pkgs/http-auto"
	slices "github.com/taubyte/utils/slices/string"
)

func Start(ctx context.Context, protocolConfig *config.Protocol) error {
	log.SetAllLoggers(log.LevelFatal)

	ctx, ctx_cancel := context.WithCancel(ctx)
	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		fmt.Println("Exiting... Odo")
		ctx_cancel()
	}()

	databasePath := config.DatabasePath + protocolConfig.Shape

	if protocolConfig.DevMode {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		protocolConfig.Root = dir
		databasePath = protocolConfig.Shape
	}
	protocolConfig.Verbose = true

	err := createP2PNodes(ctx, databasePath, protocolConfig.Shape, protocolConfig)
	if err != nil {
		return err
	}

	// Create httpNode if needed
	var httpNode httpService.Service
	for _, srv := range protocolConfig.Protocols {
		if slices.Contains(commonSpecs.HttpProtocols, srv) {
			httpNode, err = auto.Configure(protocolConfig).AutoHttp(protocolConfig.Node)
			if err != nil {
				return fmt.Errorf("new autoHttp failed with: %s", err)
			}

			protocolConfig.Http = httpNode
			break
		}
	}

	// Attach any p2p/http endpoints
	var includesNode bool
	services := make([]services.Service, 0)
	for _, srv := range protocolConfig.Protocols {
		if srv == "node" {
			includesNode = true
			continue
		}

		srvPkg, ok := available[srv]
		if !ok {
			return fmt.Errorf("services `%s` does not exist ", srv)
		}

		_srv, err := srvPkg.New(ctx, protocolConfig)
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

		_srv, err := srvPkg.New(ctx, protocolConfig)
		if err != nil {
			return fmt.Errorf("new for node failed with: %s", err)
		}

		services = append(services, _srv)
	}

	if httpNode != nil {
		httpNode.Start()
	}

	// TODO: Use logger
	fmt.Printf("%s started! with id: %s\n", protocolConfig.Shape, protocolConfig.Node.ID())

	// https://github.com/ipfs/go-ipfs/blob/8f623c9124d6c0b1d511a072a4d13633884c7b40/core/builder.go

	<-ctx.Done()
	for _, srv := range services {
		srv.Close()
	}

	if protocolConfig.ClientNode != nil {
		protocolConfig.ClientNode.Close()
	}

	if protocolConfig.Node != nil {
		protocolConfig.Node.Close()
	}

	if protocolConfig.Http != nil {
		protocolConfig.Http.Stop()
	}

	return nil
}
