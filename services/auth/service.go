package auth

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	accountsClientPkg "github.com/taubyte/tau/clients/p2p/accounts"
	tnsApi "github.com/taubyte/tau/clients/p2p/tns"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	streams "github.com/taubyte/tau/p2p/streams/service"
	tauConfig "github.com/taubyte/tau/pkg/config"
	"github.com/taubyte/tau/pkg/kvdb"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/common/httpsvc"
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
		srv.node, err = tauConfig.NewNode(ctx, cfg, path.Join(cfg.Root(), servicesCommon.Auth))
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
	if srv.db, err = srv.dbFactory.New(logger, servicesCommon.Auth, rebroadcastInterval); err != nil {
		return nil, err
	}
	if srv.tnsClient, err = tnsApi.New(srv.ctx, clientNode); err != nil {
		return nil, err
	}
	srv.rootDomain = cfg.NetworkFqdn()
	if srv.stream, err = streams.New(srv.node, servicesCommon.Auth, servicesCommon.AuthProtocol); err != nil {
		return nil, err
	}
	srv.hostUrl = cfg.NetworkFqdn()
	nodePath := path.Join(cfg.Root(), servicesCommon.Auth)
	if srv.secretsService, err = initSecretsService(srv.db, srv.node, nodePath); err != nil {
		return nil, err
	}

	if accountsIface.VerifyOnAuth {
		srv.accountsURL = accountsIface.InferURL(cfg.DevMode(), cfg.NetworkFqdn())
		var ac error
		srv.accountsClient, ac = accountsClientPkg.New(srv.ctx, clientNode)
		if ac != nil {
			return nil, fmt.Errorf("creating accounts client failed with %s", ac)
		}
	}

	srv.setupStreamRoutes()
	srv.stream.Start()

	if srv.http = cfg.Http(); srv.http == nil {
		srv.http, err = httpsvc.New(ctx, srv.node, cfg)
		if err != nil {
			return nil, fmt.Errorf("new http failed with: %s", err)
		}
		defer srv.http.Start()
	}
	srv.setupHTTPRoutes()

	return &srv, nil
}

func (srv *AuthService) Close() error {
	logger.Info("Closing", servicesCommon.Auth)
	defer logger.Info(servicesCommon.Auth, "closed")

	if srv.secretsService != nil {
		srv.secretsService.Close()
	}
	if srv.accountsClient != nil {
		srv.accountsClient.Close()
	}

	srv.stream.Stop()
	srv.tnsClient.Close()
	srv.db.Close()

	return nil
}
