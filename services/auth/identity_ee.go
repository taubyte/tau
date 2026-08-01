//go:build ee

package auth

import (
	"context"

	eeauth "github.com/taubyte/tau/ee/auth"
	tauConfig "github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
)

// identity is what this build answers callers through. The type is defined
// once in the ee tree; this file only names it.
type identity = *eeauth.Identity

func (srv *AuthService) closeIdentity() { srv.identity.Close() }

// initIdentity builds the provider. tenancy is read but not required here;
// this build answers through the provider instead.
func (srv *AuthService) initIdentity(cfg tauConfig.Config) error {
	srv.tenancy = cfg.Tenancy()

	var err error
	srv.identity, err = eeauth.NewIdentity(srv.ctx, srv.identityClientNode, cfg)
	return err
}

func (srv *AuthService) authorizeIdentity(rctx context.Context, ctx http.Context, client GitHubClient) error {
	return srv.identity.Authorize(rctx, ctx, client)
}

func (srv *AuthService) authorizeRepository(client GitHubClient) error {
	return srv.identity.AuthorizeRepository(client)
}
