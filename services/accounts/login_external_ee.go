//go:build ee

package accounts

import (
	"context"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/ee/services/accounts/idp/oidc"
)

// startExternalLogin (EE build) delegates to ee/services/accounts/idp/oidc.
// In v1, the OIDC implementation itself is a stub returning
// "OIDC implementation not yet shipped"; the build-tag seam exists so
// operators can already configure Accounts with auth_mode=external_oidc and
// EE customers see the seam fail explicitly rather than silently.
func (srv *AccountsService) startExternalLogin(ctx context.Context, accountSlug string) (*accountsIface.ExternalLoginRedirect, error) {
	return oidc.Start(ctx, accountSlug)
}

// finishExternalLogin (EE build) delegates to ee/services/accounts/idp/oidc.
func (srv *AccountsService) finishExternalLogin(ctx context.Context, in accountsIface.FinishExternalLoginInput) (*accountsIface.Session, error) {
	return oidc.Finish(ctx, in)
}
