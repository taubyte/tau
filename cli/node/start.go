package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/taubyte/tau/config"
	"github.com/taubyte/tau/core/services"
	httpService "github.com/taubyte/tau/pkg/http"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/kvdb"
	commonSpecs "github.com/taubyte/tau/pkg/specs/common"
	slices "github.com/taubyte/utils/slices/string"
)

func Start(ctx context.Context, protocolConfig *config.Node) error {
	setLevel()

	ctx, ctx_cancel := context.WithCancel(ctx)
	sigkill := make(chan os.Signal, 1)
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigkill
		logger.Info("Exiting... Tau")
		ctx_cancel()
	}()

	storagePath := protocolConfig.Root + "/storage/" + protocolConfig.Shape

	err := createNodes(ctx, storagePath, protocolConfig.Shape, protocolConfig)
	if err != nil {
		return err
	}

	protocolConfig.Databases = kvdb.New(protocolConfig.Node)

	// Create httpNode if needed
	var httpNode httpService.Service
	for _, srv := range protocolConfig.Services {
		if slices.Contains(commonSpecs.HTTPServices, srv) {
			httpNode, err = auto.NewAuto(ctx, protocolConfig.Node, protocolConfig)
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
	for _, srv := range protocolConfig.Services {
		if srv == "substrate" {
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
		srvPkg, ok := available["substrate"]
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

	logger.Infof("%s started! with id: %s", protocolConfig.Shape, protocolConfig.Node.ID())

	<-ctx.Done()
	for _, srv := range services {
		srv.Close()
	}

	if protocolConfig.ClientNode != nil {
		protocolConfig.ClientNode.Close()
	}

	if protocolConfig.Databases != nil {
		protocolConfig.Databases.Close()
	}

	if protocolConfig.Node != nil {
		protocolConfig.Node.Close()
	}

	if protocolConfig.Http != nil {
		protocolConfig.Http.Stop()
	}

	return nil
}
