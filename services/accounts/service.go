package accounts

import (
	"context"
	"errors"
	"fmt"
	"path"

	"github.com/ipfs/go-log/v2"
	kvdbIface "github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"
	tauConfig "github.com/taubyte/tau/pkg/config"
	kvdbpkg "github.com/taubyte/tau/pkg/kvdb"
	servicesCommon "github.com/taubyte/tau/services/common"
	"github.com/taubyte/tau/services/common/httpsvc"
)

var (
	logger = log.Logger("tau.accounts.service")
)

// New constructs an AccountsService. Mirrors services/auth/service.go:New so
// wiring in cli/node/node.go is uniform.
func New(ctx context.Context, cfg tauConfig.Config) (*AccountsService, error) {
	var srv AccountsService
	srv.ctx = ctx
	srv.config = cfg
	srv.devMode = cfg.DevMode()
	srv.cfg = newAccountsConfig(cfg.Accounts(), cfg.NetworkFqdn())
	srv.accountsURL = accountsIface.InferURL(srv.devMode, cfg.NetworkFqdn())

	var err error

	if srv.node = cfg.Node(); srv.node == nil {
		srv.node, err = tauConfig.NewNode(ctx, cfg, path.Join(cfg.Root(), servicesCommon.Accounts))
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
		srv.dbFactory = kvdbpkg.New(srv.node)
	}

	rebroadcastInterval := 5
	if srv.devMode {
		rebroadcastInterval = 1
	}
	if srv.db, err = srv.dbFactory.New(logger, servicesCommon.Accounts, rebroadcastInterval); err != nil {
		return nil, err
	}

	if srv.stream, err = streams.New(srv.node, servicesCommon.Accounts, servicesCommon.AccountsProtocol); err != nil {
		return nil, err
	}

	// Auth subsystems are optional struct-wise; the login surface errors
	// when its dependencies are absent.
	if err = srv.initAuthSubsystems(); err != nil {
		return nil, err
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

// Close releases resources held by the service.
func (srv *AccountsService) Close() error {
	logger.Info("Closing", servicesCommon.Accounts)
	defer logger.Info(servicesCommon.Accounts, "closed")

	if srv.stream != nil {
		srv.stream.Stop()
	}
	if srv.db != nil {
		srv.db.Close()
	}
	return nil
}

// Node returns the underlying p2p node (services.Service).
func (srv *AccountsService) Node() peer.Node { return srv.node }

// KV returns the keyvalue store (services.DBService).
func (srv *AccountsService) KV() kvdbIface.KVDB { return srv.db }

// Client returns an in-process Client.
func (srv *AccountsService) Client() accountsIface.Client {
	if srv.client == nil {
		srv.client = newInProcessClient(srv)
	}
	return srv.client
}

// newAccountsConfig snapshots the relevant accounts config for handler use.
// Empty `From` defaults to `noreply@<rootDomain>` (or `noreply@localhost` in
// dev / when the FQDN is unset) so operators don't have to set a value just
// to satisfy the From: header.
func newAccountsConfig(in tauConfig.Accounts, rootDomain string) accountsConfig {
	from := in.Email.SMTP.From
	if from == "" {
		if rootDomain != "" {
			from = "noreply@" + rootDomain
		} else {
			from = "noreply@localhost"
		}
	}
	return accountsConfig{
		sessionTTL: in.SessionTTL,

		emailSMTPHost: in.Email.SMTP.Host,
		emailSMTPPort: in.Email.SMTP.Port,
		emailSMTPUser: in.Email.SMTP.User,
		emailSMTPPass: in.Email.SMTP.Pass,
		emailSMTPFrom: from,
	}
}
