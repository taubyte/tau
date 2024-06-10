package auth

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	tauConfig "github.com/taubyte/tau/config"
	seerIface "github.com/taubyte/tau/core/services/seer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/kvdb"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var (
	logger = log.Logger("tau.auth.service")
)

func New(ctx context.Context, config *tauConfig.Node) (*AuthService, error) {
	var srv AuthService
	srv.ctx = ctx

	if config == nil {
		config = &tauConfig.Node{}
	}

	srv.webHookUrl = fmt.Sprintf(`https://patrick.tau.%s`, config.NetworkFqdn)

	err := config.Validate()
	if err != nil {
		return nil, err
	}

	srv.devMode = config.DevMode
	if srv.devMode {
		deployKeyName = devDeployKeyName
	}

	if config.Node == nil {
		srv.node, err = tauConfig.NewNode(ctx, config, path.Join(config.Root, protocolCommon.Auth))
		if err != nil {
			return nil, err
		}
	} else {
		if len(config.DomainValidation.PrivateKey) == 0 || len(config.DomainValidation.PublicKey) == 0 {
			return nil, errors.New("private and public key cannot be empty")
		}

		srv.node = config.Node
	}

	srv.dbFactory = config.Databases
	if srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	srv.dvPrivateKey = config.DomainValidation.PrivateKey
	srv.dvPublicKey = config.DomainValidation.PublicKey

	clientNode := srv.node
	if config.ClientNode != nil {
		clientNode = config.ClientNode
	}

	srv.db, err = srv.dbFactory.New(logger, protocolCommon.Auth, 5)
	if err != nil {
		return nil, err
	}

	srv.tnsClient, err = tnsApi.New(srv.ctx, clientNode)
	if err != nil {
		return nil, err
	}
	// should end if any of the two contexts ends

	srv.rootDomain = config.NetworkFqdn

	// P2P
	srv.stream, err = streams.New(srv.node, protocolCommon.Auth, protocolCommon.AuthProtocol)
	if err != nil {
		return nil, err
	}

	srv.hostUrl = config.NetworkFqdn
	srv.setupStreamRoutes()

	sc, err := seerClient.New(ctx, clientNode)
	if err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}

	err = protocolCommon.StartSeerBeacon(config, sc, seerIface.ServiceTypeAuth)
	if err != nil {
		return nil, err
	}

	if config.Http == nil {
		srv.http, err = auto.NewBasic(ctx, config)
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
	logger.Info("Closing", protocolCommon.Auth)
	defer logger.Info(protocolCommon.Auth, "closed")

	srv.stream.Stop()
	srv.tnsClient.Close()
	srv.db.Close()

	return nil
}
