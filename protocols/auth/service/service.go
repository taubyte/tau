package service

import (
	"context"
	"errors"
	"fmt"

	moody "bitbucket.org/taubyte/go-moody-blues"
	dreamlandCommon "github.com/taubyte/dreamland/core/common"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	seerClient "github.com/taubyte/odo/clients/p2p/seer"
	tnsApi "github.com/taubyte/odo/clients/p2p/tns"
	odoConfig "github.com/taubyte/odo/config"
	auto "github.com/taubyte/odo/pkgs/http-auto"
	kv "github.com/taubyte/odo/pkgs/kvdb/database"
	streams "github.com/taubyte/p2p/streams/service"

	protocolCommon "github.com/taubyte/odo/protocols/common"
)

var (
	logger, _ = moody.New("auth.service")
)

func New(ctx context.Context, config *odoConfig.Protocol) (*AuthService, error) {
	var srv AuthService
	srv.ctx = ctx

	if config == nil {
		config = &odoConfig.Protocol{}
	}

	srv.webHookUrl = fmt.Sprintf(`https://patrick.tau.%s`, config.NetworkUrl)

	err := config.Build(odoConfig.ConfigBuilder{
		DefaultP2PListenPort: protocolCommon.AuthDefaultP2PListenPort,
		DevHttpListenPort:    protocolCommon.AuthDevHttpListenPort,
		DevP2PListenFormat:   dreamlandCommon.DefaultP2PListenFormat,
	})
	if err != nil {
		return nil, err
	}

	srv.devMode = config.DevMode
	if srv.devMode {
		deployKeyName = devDeployKeyName
	}

	if config.Node == nil {
		srv.node, err = odoConfig.NewNode(ctx, config, protocolCommon.Auth)
		if err != nil {
			return nil, err
		}
	} else {
		if len(config.DomainValidation.PrivateKey) == 0 || len(config.DomainValidation.PublicKey) == 0 {
			return nil, errors.New("private and public key cannot be empty")
		}

		srv.node = config.Node
	}

	srv.dvPrivateKey = config.DomainValidation.PrivateKey
	srv.dvPublicKey = config.DomainValidation.PublicKey

	// For Odo
	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	srv.db, err = kv.New(logger.Std(), srv.node, protocolCommon.Auth, 5)
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
	srv.stream, err = streams.New(srv.node, protocolCommon.Auth, protocolCommon.AuthProtocol)
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
	fmt.Println("Closing", protocolCommon.Auth)
	defer fmt.Println(protocolCommon.Auth, "closed")

	// node.ctx
	srv.stream.Stop()

	// ctx & partly relies on node
	srv.tnsClient.Close()

	// ctx, needs to close after node as node will try to close it's store
	srv.db.Close()

	return nil
}
