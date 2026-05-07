package accounts

import (
	"context"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// loginDispatcher routes accountsIface.Login methods to managed-mode (passkey
// + magic-link) and external-mode (OIDC/SAML, EE-only) handlers. The managed
// path lives in login_managed.go and is universal; the external path goes
// through the build-tag seam (login_external{,_ee}.go).
type loginDispatcher struct {
	srv     *AccountsService
	managed *loginManaged
}

// StartManaged begins managed-mode authentication.
func (d *loginDispatcher) StartManaged(ctx context.Context, in accountsIface.StartManagedLoginInput) (*accountsIface.ManagedLoginChallenge, error) {
	return d.managed.StartManaged(ctx, in)
}

// FinishManagedPasskey completes a passkey login.
func (d *loginDispatcher) FinishManagedPasskey(ctx context.Context, in accountsIface.FinishPasskeyInput) (*accountsIface.Session, error) {
	return d.managed.FinishManagedPasskey(ctx, in)
}

// FinishManagedMagicLink completes a magic-link login.
func (d *loginDispatcher) FinishManagedMagicLink(ctx context.Context, in accountsIface.FinishMagicLinkInput) (*accountsIface.Session, error) {
	return d.managed.FinishManagedMagicLink(ctx, in)
}

// StartExternal routes external-mode logins (OIDC/SAML) through the build-tag
// seam. Community returns "external auth modes require Enterprise Edition";
// EE delegates to ee/services/accounts/idp/...
func (d *loginDispatcher) StartExternal(ctx context.Context, accountSlug string) (*accountsIface.ExternalLoginRedirect, error) {
	return d.srv.startExternalLogin(ctx, accountSlug)
}

// FinishExternal accepts the IdP callback. Same seam pattern as StartExternal.
func (d *loginDispatcher) FinishExternal(ctx context.Context, in accountsIface.FinishExternalLoginInput) (*accountsIface.Session, error) {
	return d.srv.finishExternalLogin(ctx, in)
}

// VerifySession verifies a Member-session bearer.
func (d *loginDispatcher) VerifySession(ctx context.Context, token string) (*accountsIface.Session, error) {
	return d.managed.VerifySession(ctx, token)
}

// Logout revokes a Member-session bearer.
func (d *loginDispatcher) Logout(ctx context.Context, token string) error {
	return d.managed.Logout(ctx, token)
}
