package service

import (
	"context"
	"errors"
	"fmt"

	kv "bitbucket.org/taubyte/kvdb/database"
	configutils "bitbucket.org/taubyte/p2p/config"
	streams "bitbucket.org/taubyte/p2p/streams/service"

	dreamlandCommon "bitbucket.org/taubyte/dreamland/common"
	moody "bitbucket.org/taubyte/go-moody-blues"
	auto "bitbucket.org/taubyte/http-auto"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	seerClient "github.com/taubyte/odo/clients/p2p/seer"
	tnsApi "github.com/taubyte/odo/clients/p2p/tns"
	common "github.com/taubyte/odo/protocols/auth/common"

	commonIface "github.com/taubyte/go-interfaces/services/common"
)

var (
	logger, _ = moody.New("auth.service")
)

func New(ctx context.Context, config *commonIface.GenericConfig) (*AuthService, error) {
	var srv AuthService
	srv.ctx = ctx

	if config == nil {
		_cnf := &commonIface.GenericConfig{}
		_cnf.Bootstrap = true

		config = _cnf
	}

	srv.webHookUrl = fmt.Sprintf(`https://patrick.tau.%s`, config.NetworkUrl)

	err := config.Build(commonIface.ConfigBuilder{
		DefaultP2PListenPort: common.DefaultP2PListenPort,
		DevHttpListenPort:    common.DevHttpListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, err
	}

	srv.devMode = config.DevMode
	if srv.devMode {
		common.DeployKeyName = common.DevDeployKeyName
	}

	if config.Node == nil {
		srv.node, err = configutils.NewNode(ctx, config, common.DatabaseName)
		if err != nil {
			return nil, err
		}
	} else {
		if len(config.DVPrivateKey) == 0 || len(config.DVPublicKey) == 0 {
			return nil, errors.New("private and public key cannot be empty")
		}

		srv.node = config.Node
	}

	srv.dvPrivateKey = config.DVPrivateKey
	srv.dvPublicKey = config.DVPublicKey

	// For Odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	srv.db, err = kv.New(logger.Std(), srv.node, common.DatabaseName, 5)
	if err != nil {
		return nil, err
	}

	srv.tnsClient, err = tnsApi.New(srv.ctx, clientNode)
	if err != nil {
		return nil, err
	}
	// should end if any of the two contexts ends

	srv.rootDomain = config.NetworkUrl

	// P2P
	srv.stream, err = streams.New(srv.node, common.ServiceName, common.Protocol)
	if err != nil {
		return nil, err
	}

	srv.hostUrl = config.NetworkUrl
	srv.setupStreamRoutes()

	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = config.StartSeerBeacon(sc, seerIface.ServiceTypeAuth)
	if err != nil {
		return nil, err
	}

	// Not writing if deploying a network
	if commonIface.Deployment != commonIface.Odo {
		// HACK: Make sure certificates there
		// tls.WriteCerts()
	}

	if config.Http == nil {
		srv.http, err = auto.Configure(config).BasicHttp(ctx)
		if err != nil {
			return nil, fmt.Errorf("new http failed with: %s", err)
		}
	} else {
		srv.http = config.Http
	}

	srv.setupHTTPRoutes()

	if config.Http == nil {
		srv.http.Start()
	}

	return &srv, nil
}

func (srv *AuthService) Close() error {
	// TODO use debug logger
	fmt.Println("Closing", common.DatabaseName)
	defer fmt.Println(common.DatabaseName, "closed")

	// node.ctx
	srv.stream.Stop()

	// ctx & partly relies on node
	srv.tnsClient.Close()

	// ctx, needs to close after node as node will try to close it's store
	srv.db.Close()

	// ctx
	srv.node.Close()

	// ctx
	srv.http.Stop()
	return nil
}
