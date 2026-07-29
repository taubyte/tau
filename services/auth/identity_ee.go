//go:build ee

package auth

import (
	"context"
	"errors"
	"fmt"

	eeauth "github.com/taubyte/tau/ee/auth"
	tauConfig "github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
)

// identityClient is what this build resolves identity through.
type identityClient = *eeauth.Identity

func (srv *AuthService) closeIdentity() { srv.accountsClient.Close() }

// initIdentity builds the provider. tenancy is read but not required here;
// this build resolves identity through the provider instead.
func (srv *AuthService) initIdentity(cfg tauConfig.Config) error {
	srv.tenancy = cfg.Tenancy()

	var err error
	srv.accountsClient, err = eeauth.NewIdentity(srv.ctx, srv.identityClientNode, cfg)
	return err
}

// authorizeIdentity answers whether the caller may use this cloud's API.
// Whatever the provider resolves is stashed on the http context for downstream
// use; this seam does not inspect it.
func (srv *AuthService) authorizeIdentity(rctx context.Context, ctx http.Context, client GitHubClient) error {
	gh := client.Me()
	if gh == nil || gh.ID == nil {
		return errors.New("github user identity unavailable")
	}

	resolved, err := srv.accountsClient.Authorize(rctx, "github", fmt.Sprintf("%d", *gh.ID))
	if err != nil {
		return err
	}
	if resolved != nil {
		ctx.SetVariable("LinkedAccounts", resolved)
	}
	return nil
}

// authorizeRepository is a no-op in this build: which repositories may be
// registered follows from the identity resolved above.
func (srv *AuthService) authorizeRepository(GitHubClient) error { return nil }
