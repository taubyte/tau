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
	"github.com/taubyte/tau/config"
	auto "github.com/taubyte/tau/pkgs/http-auto"
	"github.com/taubyte/tau/pkgs/kvdb"
	slices "github.com/taubyte/utils/slices/string"
)

// Starts a node service
func Start(ctx context.Context, protocolConfig *config.Node) error {
	//inits logger
	log.SetAllLoggers(log.LevelFatal)

	//context init
	ctx, ctx_cancel := context.WithCancel(ctx)
	//kill channel for shutting down go routines
	sigkill := make(chan os.Signal, 1)
	//kill channel trigger
	signal.Notify(sigkill, os.Interrupt, syscall.SIGTERM)

	//go routine to handle shut down
	go func() {
		<-sigkill
		logger.Info("Exiting... Odo")
		ctx_cancel()
	}()

	//database file path
	storagePath := protocolConfig.Root + "/storage/" + protocolConfig.Shape

	//create nodes defined in the config
	err := createNodes(ctx, storagePath, protocolConfig.Shape, protocolConfig)
	if err != nil {
		return err
	}

	//sets database connection info for a node
	protocolConfig.Databases = kvdb.New(protocolConfig.Node)

	// Create httpNode if needed
	var httpNode httpService.Service

	//cycles through the []string of protocols to see if any contain a value in the  HttpProcools []string,
	// if it does, create a new httpnode, set it in the config, and break the loop
	for _, srv := range protocolConfig.Protocols {
		if slices.Contains(commonSpecs.HttpProtocols, srv) {
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
	//cycles through the protocols again to attach p2p/http endpoints
	for _, srv := range protocolConfig.Protocols {
		if srv == "substrate" {
			includesNode = true
			continue
		}

		//checks map for service to see verify it exists
		srvPkg, ok := available[srv]
		if !ok {
			return fmt.Errorf("services `%s` does not exist ", srv)
		}

		//creates new service and errors out if it fails
		_srv, err := srvPkg.New(ctx, protocolConfig)
		if err != nil {
			return fmt.Errorf("new for service `%s` failed with: %s", srv, err)
		}

		//appends new node service to the [] of services
		services = append(services, _srv)
	}
	//The 2 above for loops could be combined to prevent double cycling through the protocols like so:
	/*
		var httpNode httpService.Service
		var includesNode bool
		services := make([]services.Service, 0)
		for _, srv := range protocolConfig.Protocols {
			if httpNode == nil {
				if slices.Contains(commonSpecs.HttpProtocols, srv) {
					httpNode, err = auto.NewAuto(ctx, protocolConfig.Node, protocolConfig)
					if err != nil {
						return fmt.Errorf("new autoHttp failed with: %s", err)
					}

					protocolConfig.Http = httpNode
				}
			}
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
	*/

	// Running node last if included in list
	if includesNode {
		//checks for node within service package
		srvPkg, ok := available["substrate"]
		if !ok {
			return errors.New("node was not found in available packages")
		}

		//creates new node and errors out if it fails
		_srv, err := srvPkg.New(ctx, protocolConfig)
		if err != nil {
			return fmt.Errorf("new for node failed with: %s", err)
		}

		//appends new node service to the [] of services
		services = append(services, _srv)
	}

	//starts the http node if necassary
	if httpNode != nil {
		httpNode.Start()
	}

	logger.Infof("%s started! with id: %s", protocolConfig.Shape, protocolConfig.Node.ID())

	//runs till the shut down signal is sent through the context
	<-ctx.Done()
	//shutdowns all started services
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
