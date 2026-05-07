package accounts

import (
	"errors"
	"fmt"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/services/accounts/email"
)

// initAuthSubsystems wires the auth-side stores onto the service. Ordering:
// sessions → email → magic-link → webauthn.
//
// Email-sender selection:
//   - SMTP fully configured → SMTPSender (production).
//   - SMTP not configured + DevMode → StdoutSender (dev/dream).
//   - SMTP not configured + production → error, so the operator notices
//     before users get stuck mid-login.
func (srv *AccountsService) initAuthSubsystems() error {
	// Sessions (always available).
	srv.sessions = newSessionStore(srv.db, parseSessionTTL(srv.cfg.sessionTTL))

	// Email sender. Stdout fallback is auto-enabled in DevMode rather than
	// gated by an explicit config flag — the only legitimate "stdout in
	// prod" use case is debugging, and DevMode is the right knob for that.
	sender, err := selectEmailSender(srv.cfg, srv.devMode)
	if err != nil {
		return err
	}

	// Magic-link store. Pull the URL from the service struct (where it was
	// set by `inferAccountsURL` at construction) rather than from the
	// snapshotted config — the config slice doesn't carry the URL anymore
	// since it's derived from NetworkFqdn rather than configured directly.
	// Rate limits are hardcoded inside `magicLinkStore` (5/email/hr,
	// 20/IP/hr); operators can't accidentally weaken them.
	srv.magicLink = newMagicLinkStore(srv.db, sender, srv.accountsURL)

	// WebAuthn relying-party. Derived from NetworkFqdn (same source as the
	// HTTP host the requests will land on); no operator config involved.
	wa, err := newWebAuthnStore(srv.db, accountsIface.InferWebAuthn(srv.devMode, srv.rootDomain), func(accountID string) *memberStore {
		return newMemberStore(srv.db, accountID)
	})
	if err != nil {
		return err
	}
	srv.webAuthn = wa

	return nil
}

// selectEmailSender picks SMTP, stdout, or returns an error per the rules
// above. Dev / dream installs hit the stdout path automatically.
func selectEmailSender(cfg accountsConfig, devMode bool) (email.Sender, error) {
	if cfg.emailSMTPHost != "" {
		return email.NewSMTPSender(cfg.emailSMTPHost, cfg.emailSMTPPort, cfg.emailSMTPUser, cfg.emailSMTPPass, cfg.emailSMTPFrom)
	}
	if devMode {
		return email.NewStdoutSender(nil), nil
	}
	return nil, errors.New("accounts: SMTP not configured and DevMode=false; refusing to start")
}

// parseSessionTTL turns the config string ("24h", "168h", "") into a duration.
// Empty returns 0, which the session store interprets as "default 24h".
func parseSessionTTL(s string) time.Duration {
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		// Surface a clear runtime warning rather than silently picking a
		// default the operator didn't ask for.
		panic(fmt.Errorf("accounts: invalid Accounts.SessionTTL %q: %w", s, err))
	}
	return d
}
