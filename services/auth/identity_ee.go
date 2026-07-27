//go:build ee

package auth

import (
	"context"
	"errors"
	"fmt"

	accountsClientPkg "github.com/taubyte/tau/clients/p2p/accounts"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	tauConfig "github.com/taubyte/tau/pkg/config"
	http "github.com/taubyte/tau/pkg/http"
)

// initIdentity wires the client this build answers identity questions with.
// tenancy is read but not required here; this build resolves identity through
// the accounts service instead.
func (srv *AuthService) initIdentity(cfg tauConfig.Config) error {
	srv.tenancy = cfg.Tenancy()

	if !accountsIface.VerifyOnAuth {
		return nil
	}

	srv.accountsURL = accountsIface.InferURL(cfg.DevMode(), cfg.NetworkFqdn())

	var err error
	if srv.accountsClient, err = accountsClientPkg.New(srv.ctx, srv.identityClientNode); err != nil {
		return fmt.Errorf("creating accounts client failed with %s", err)
	}

	return nil
}

// authorizeIdentity answers whether the caller may use this cloud's API. Linked
// accounts are stashed on the http context for downstream use.
func (srv *AuthService) authorizeIdentity(rctx context.Context, ctx http.Context, client GitHubClient) error {
	if srv.accountsClient == nil {
		return nil
	}

	gh := client.Me()
	if gh == nil || gh.ID == nil {
		return errors.New("github user identity unavailable")
	}

	externalID := fmt.Sprintf("%d", *gh.ID)
	vresp, err := srv.accountsClient.Verify(rctx, "github", externalID)
	if err != nil {
		return fmt.Errorf("accounts verify failed: %w", err)
	}
	if !vresp.Linked {
		if srv.accountsURL != "" {
			return fmt.Errorf("no tau account linked to this github identity — sign up at %s", srv.accountsURL)
		}
		return errors.New("no tau account linked to this github identity")
	}

	ctx.SetVariable("LinkedAccounts", vresp.Accounts)
	return nil
}

// authorizeRepository is a no-op in this build. Which repositories may be
// registered follows from the identity resolved above, not from a namespace
// named in the shape config.
func (srv *AuthService) authorizeRepository(GitHubClient) error { return nil }
