//go:build !ee

package accounts

import (
	"context"
	"errors"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// errExternalRequiresEE is the canonical rejection text for external auth
// modes (Okta / generic OIDC / SAML) when running the community build.
//
// The same text appears in the EE build's stub for not-yet-shipped
// providers — it's the operator-visible signal that the feature exists but
// requires the Enterprise build to enable.
var errExternalRequiresEE = errors.New("external auth modes require Enterprise Edition")

// startExternalLogin is the community stub. Returns the canonical error
// regardless of the Account's auth_mode — operators should switch the
// Account to managed mode or build with -tags=ee.
func (srv *AccountsService) startExternalLogin(_ context.Context, _ string) (*accountsIface.ExternalLoginRedirect, error) {
	return nil, errExternalRequiresEE
}

// finishExternalLogin is the community stub. See startExternalLogin.
func (srv *AccountsService) finishExternalLogin(_ context.Context, _ accountsIface.FinishExternalLoginInput) (*accountsIface.Session, error) {
	return nil, errExternalRequiresEE
}
