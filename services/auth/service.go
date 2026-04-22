package auth

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	seerClient "github.com/taubyte/tau/clients/p2p/seer"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	seerIface "github.com/taubyte/tau/core/services/seer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	tauConfig "github.com/taubyte/tau/pkg/config"
	auto "github.com/taubyte/tau/pkg/http-auto"
	"github.com/taubyte/tau/pkg/kvdb"
	protocolCommon "github.com/taubyte/tau/services/common"
)

var (
	logger = log.Logger("tau.auth.service")
)

func New(ctx context.Context, cfg tauConfig.Config) (*AuthService, error) {
	var srv AuthService
	srv.ctx = ctx

	srv.newGitHubClient = NewGitHubClient

	srv.webHookUrl = fmt.Sprintf(`https://patrick.tau.%s`, cfg.NetworkFqdn())

	var err error
	srv.devMode = cfg.DevMode()
	if srv.devMode {
		deployKeyName = devDeployKeyName
	}

	if srv.node = cfg.Node(); srv.node == nil {
		srv.node, err = tauConfig.NewNode(ctx, cfg, path.Join(cfg.Root(), protocolCommon.Auth))
		if err != nil {
			return nil, err
		}
	} else {
		dv := cfg.DomainValidation()
		if len(dv.PrivateKey) == 0 || len(dv.PublicKey) == 0 {
			return nil, errors.New("private and public key cannot be empty")
		}
	}

	if srv.dbFactory = cfg.Databases(); srv.dbFactory == nil {
		srv.dbFactory = kvdb.New(srv.node)
	}

	dv := cfg.DomainValidation()
	srv.dvPrivateKey = dv.PrivateKey
	srv.dvPublicKey = dv.PublicKey

	clientNode := srv.node
	if cfg.ClientNode() != nil {
		clientNode = cfg.ClientNode()
	}

	rebroadcastInterval := 5
	if srv.devMode {
		rebroadcastInterval = 1
	}
	if srv.db, err = srv.dbFactory.New(logger, protocolCommon.Auth, rebroadcastInterval); err != nil {
		return nil, err
	}
	if srv.tnsClient, err = tnsApi.New(srv.ctx, clientNode); err != nil {
		return nil, err
	}
	srv.rootDomain = cfg.NetworkFqdn()
	if srv.stream, err = streams.New(srv.node, protocolCommon.Auth, protocolCommon.AuthProtocol); err != nil {
		return nil, err
	}
	srv.hostUrl = cfg.NetworkFqdn()
	nodePath := path.Join(cfg.Root(), protocolCommon.Auth)
	if srv.secretsService, err = initSecretsService(srv.db, srv.node, nodePath); err != nil {
		return nil, err
	}
	srv.setupStreamRoutes()
	srv.stream.Start()
	var sc seerIface.Client
	if sc, err = seerClient.New(ctx, clientNode, cfg.SensorsRegistry()); err != nil {
		return nil, fmt.Errorf("creating seer client failed with %s", err)
	}
	if err = protocolCommon.StartSeerBeacon(cfg, sc, seerIface.ServiceTypeAuth); err != nil {
		return nil, err
	}

	if srv.http = cfg.Http(); srv.http == nil {
		srv.http, err = auto.New(ctx, srv.node, cfg)
		if err != nil {
			return nil, fmt.Errorf("new http failed with: %s", err)
		}
		defer srv.http.Start()
	}
	srv.setupHTTPRoutes()

	return &srv, nil
}

func (srv *AuthService) Close() error {
	logger.Info("Closing", protocolCommon.Auth)
	defer logger.Info(protocolCommon.Auth, "closed")

	if srv.secretsService != nil {
		srv.secretsService.Close()
	}

	srv.stream.Stop()
	srv.tnsClient.Close()
	srv.db.Close()

	return nil
}
