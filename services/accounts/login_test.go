package accounts

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/services/accounts/email"
)

// loginTestService wires a service with the auth subsystems but bypasses the
// node / stream / http machinery from service.New. Lets the login dispatcher
// be exercised in pure unit tests.
func loginTestService(t *testing.T) (*AccountsService, *email.StdoutSender) {
	t.Helper()
	srv := newTestService(t)
	var buf bytes.Buffer
	sender := email.NewStdoutSender(&buf)
	srv.accountsURL = "https://accounts.test.tau"
	srv.cfg = accountsConfig{}
	srv.sessions = newSessionStore(srv.db, time.Hour)
	srv.magicLink = newMagicLinkStore(srv.db, sender, srv.accountsURL)
	wa, err := newWebAuthnStore(srv.db, accountsIface.WebAuthnDefaults{}, func(accountID string) *memberStore {
		return newMemberStore(srv.db, accountID)
	})
	if err != nil {
		t.Fatalf("webauthn init: %v", err)
	}
	srv.webAuthn = wa
	return srv, sender
}

func TestStartManaged_NoMember_Errors(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	if _, err := cli.Login().StartManaged(ctx, accountsIface.StartManagedLoginInput{}); err == nil {
		t.Fatalf("expected error for empty input")
	}
	if _, err := cli.Login().StartManaged(ctx, accountsIface.StartManagedLoginInput{Email: "ghost@example.com"}); err == nil {
		t.Fatalf("expected error for unknown email")
	}
}

func TestStartManaged_MagicLinkPath(t *testing.T) {
	srv, sender := loginTestService(t)
	ctx := context.Background()

	// Create an Account + invite a Member via the in-process Client so the
	// login dispatcher can resolve the candidate.
	cli := newInProcessClient(srv)
	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	if _, err := cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com",
		Role:         accountsIface.RoleOwner,
	}); err != nil {
		t.Fatalf("Invite: %v", err)
	}

	// Member has no passkey → magic-link path.
	chal, err := cli.Login().StartManaged(ctx, accountsIface.StartManagedLoginInput{Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("StartManaged: %v", err)
	}
	if !chal.MagicLinkSent || len(chal.WebAuthnChallenge) != 0 {
		t.Fatalf("expected magic-link path, got %+v", chal)
	}
	if len(sender.Sent()) != 1 {
		t.Fatalf("expected one email sent, got %d", len(sender.Sent()))
	}
}

func TestStartManaged_MultipleAccounts_ReturnsCandidates(t *testing.T) {
	srv, _ := loginTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)

	// Same email on two Accounts → should return candidates.
	for _, slug := range []string{"alpha", "beta"} {
		acc, err := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: slug, Name: slug})
		if err != nil {
			t.Fatalf("Create %s: %v", slug, err)
		}
		if _, err := cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
			PrimaryEmail: "alice@example.com",
			Role:         accountsIface.RoleOwner,
		}); err != nil {
			t.Fatalf("Invite %s: %v", slug, err)
		}
	}

	chal, err := cli.Login().StartManaged(ctx, accountsIface.StartManagedLoginInput{Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("StartManaged: %v", err)
	}
	if len(chal.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(chal.Candidates))
	}
	// Narrow to one Account by passing the slug too.
	chal, err = cli.Login().StartManaged(ctx, accountsIface.StartManagedLoginInput{
		Email: "alice@example.com", AccountSlug: "alpha",
	})
	if err != nil {
		t.Fatalf("narrow StartManaged: %v", err)
	}
	if len(chal.Candidates) != 0 {
		t.Fatalf("expected narrowed flow without candidates, got %+v", chal.Candidates)
	}
}

func TestFinishMagicLink_RoundTrip(t *testing.T) {
	srv, sender := loginTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)

	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	_, _ = cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com",
		Role:         accountsIface.RoleOwner,
	})

	if _, err := cli.Login().StartManaged(ctx, accountsIface.StartManagedLoginInput{Email: "alice@example.com"}); err != nil {
		t.Fatalf("StartManaged: %v", err)
	}
	body := sender.Sent()[0].Body
	idx := strings.Index(body, "code=")
	if idx == -1 {
		t.Fatalf("no code in body: %s", body)
	}
	rest := body[idx+5:]
	end := strings.IndexAny(rest, "\n \r")
	if end == -1 {
		end = len(rest)
	}
	code := rest[:end]

	sess, err := cli.Login().FinishManagedMagicLink(ctx, accountsIface.FinishMagicLinkInput{Code: code})
	if err != nil {
		t.Fatalf("FinishManagedMagicLink: %v", err)
	}
	if sess.AccountID != acc.ID {
		t.Fatalf("session account mismatch: got %s want %s", sess.AccountID, acc.ID)
	}
	if sess.Token == "" {
		t.Fatalf("session token empty")
	}

	// Round-trip: VerifySession → same account/member.
	verified, err := cli.Login().VerifySession(ctx, sess.Token)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if verified.AccountID != acc.ID || verified.MemberID != sess.MemberID {
		t.Fatalf("VerifySession returned different identity: %+v", verified)
	}

	// Logout → subsequent VerifySession fails.
	if err := cli.Login().Logout(ctx, sess.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := cli.Login().VerifySession(ctx, sess.Token); err == nil {
		t.Fatalf("VerifySession after Logout should fail")
	}
}

func TestFinishMagicLink_BadCode(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	if _, err := cli.Login().FinishManagedMagicLink(ctx, accountsIface.FinishMagicLinkInput{Code: "nope"}); err == nil {
		t.Fatalf("expected error for bad code")
	}
}

// External login is rejected in both editions today: community returns the
// "Enterprise Edition" guard, EE returns "OIDC implementation not yet shipped"
// (the IdP integration in ee/services/accounts/idp/oidc is a follow-up). Both
// builds must surface an error from these entry points.
func TestStartExternal_AlwaysErrorsInV1(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	if _, err := cli.Login().StartExternal(ctx, "acme"); err == nil {
		t.Fatalf("expected error from StartExternal in v1")
	}
}

func TestFinishExternal_AlwaysErrorsInV1(t *testing.T) {
	srv, _ := loginTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	if _, err := cli.Login().FinishExternal(ctx, accountsIface.FinishExternalLoginInput{Code: "x"}); err == nil {
		t.Fatalf("expected error from FinishExternal in v1")
	}
}

func TestWebAuthnStore_NotConfigured(t *testing.T) {
	srv := newTestService(t)
	wa, err := newWebAuthnStore(srv.db, accountsIface.WebAuthnDefaults{}, func(string) *memberStore {
		return newMemberStore(srv.db, "")
	})
	if err != nil {
		t.Fatalf("newWebAuthnStore: %v", err)
	}
	if wa.Available() {
		t.Fatalf("expected Available()==false when RPID empty")
	}
	ctx := context.Background()
	if _, _, err := wa.BeginRegistration(ctx, "a", "m"); err == nil {
		t.Fatalf("BeginRegistration should fail when not configured")
	}
	if _, _, err := wa.BeginLogin(ctx, "a", "m"); err == nil {
		t.Fatalf("BeginLogin should fail when not configured")
	}
}

func TestWebAuthnStore_BeginRegistration(t *testing.T) {
	srv, _ := loginTestService(t)
	defaults := accountsIface.WebAuthnDefaults{
		RPID:    "test.tau",
		RPName:  "tau test",
		Origins: []string{"https://test.tau"},
	}
	wa, err := newWebAuthnStore(srv.db, defaults, func(accountID string) *memberStore {
		return newMemberStore(srv.db, accountID)
	})
	if err != nil {
		t.Fatalf("newWebAuthnStore: %v", err)
	}
	if !wa.Available() {
		t.Fatalf("expected Available()==true with RPID set")
	}

	ctx := context.Background()
	cli := newInProcessClient(srv)
	acc, _ := cli.Accounts().Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	m, _ := cli.Members(acc.ID).Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com",
	})

	sessionID, options, err := wa.BeginRegistration(ctx, acc.ID, m.ID)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	if sessionID == "" || options == nil {
		t.Fatalf("got %q %+v", sessionID, options)
	}

	// BeginLogin without registered passkeys → error.
	if _, _, err := wa.BeginLogin(ctx, acc.ID, m.ID); err == nil {
		t.Fatalf("expected BeginLogin to fail without registered passkeys")
	}
}

func TestWebAuthnStore_SessionData_Consume(t *testing.T) {
	srv, _ := loginTestService(t)
	wa := srv.webAuthn // not configured (no RPID); session-data helpers still
	// available but BeginX won't run. We test the consume path via persist
	// + consume directly.

	ctx := context.Background()
	id, err := wa.persistSessionData(ctx, nil, "acct-1", "mem-1", "register")
	if err != nil {
		t.Fatalf("persistSessionData: %v", err)
	}
	_, accID, memID, kind, err := wa.consumeSessionData(ctx, id)
	if err != nil {
		t.Fatalf("consumeSessionData: %v", err)
	}
	if accID != "acct-1" || memID != "mem-1" || kind != "register" {
		t.Fatalf("unexpected: %s / %s / %s", accID, memID, kind)
	}
	// Single-use: re-consume should fail.
	if _, _, _, _, err := wa.consumeSessionData(ctx, id); err == nil {
		t.Fatalf("expected single-use enforcement")
	}
	// Missing session → not-found error.
	if _, _, _, _, err := wa.consumeSessionData(ctx, "nope"); err == nil {
		t.Fatalf("expected not-found error")
	}
}

func TestLogin_NoSessionsStore(t *testing.T) {
	srv := newTestService(t)
	mgr := newLoginManaged(srv) // srv.sessions is nil
	ctx := context.Background()

	if _, err := mgr.VerifySession(ctx, "tok"); err == nil {
		t.Fatalf("VerifySession should error when sessions store is nil")
	}
	if err := mgr.Logout(ctx, "tok"); err == nil {
		t.Fatalf("Logout should error when sessions store is nil")
	}
	if _, err := mgr.FinishManagedMagicLink(ctx, accountsIface.FinishMagicLinkInput{Code: "x"}); err == nil {
		t.Fatalf("FinishManagedMagicLink should error when magicLink store is nil")
	}
}

func TestLogin_FinishPasskey_NoWebAuthn(t *testing.T) {
	srv, _ := loginTestService(t)
	srv.webAuthn = nil
	cli := newInProcessClient(srv)
	ctx := context.Background()
	if _, err := cli.Login().FinishManagedPasskey(ctx, accountsIface.FinishPasskeyInput{SessionID: "x"}); err == nil {
		t.Fatalf("expected error when webauthn is nil")
	}
	if _, err := cli.Login().FinishManagedPasskey(ctx, accountsIface.FinishPasskeyInput{}); err == nil {
		t.Fatalf("expected error when session_id missing")
	}
}

func TestWebAuthnStore_FinishWrongKind(t *testing.T) {
	srv, _ := loginTestService(t)
	defaults := accountsIface.WebAuthnDefaults{
		RPID:    "test.tau",
		RPName:  "tau test",
		Origins: []string{"https://test.tau"},
	}
	wa, err := newWebAuthnStore(srv.db, defaults, func(accountID string) *memberStore {
		return newMemberStore(srv.db, accountID)
	})
	if err != nil {
		t.Fatalf("newWebAuthnStore: %v", err)
	}
	ctx := context.Background()

	id, err := wa.persistSessionData(ctx, nil, "acct-1", "mem-1", "register")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, _, err := wa.FinishLogin(ctx, id, nil); err == nil {
		t.Fatalf("FinishLogin should reject register-kind session")
	}

	id, err = wa.persistSessionData(ctx, nil, "acct-1", "mem-1", "login")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := wa.FinishRegistration(ctx, id, nil); err == nil {
		t.Fatalf("FinishRegistration should reject login-kind session")
	}
}

func TestWebAuthnStore_FinishUnknownSession(t *testing.T) {
	srv, _ := loginTestService(t)
	defaults := accountsIface.WebAuthnDefaults{
		RPID:    "test.tau",
		RPName:  "tau test",
		Origins: []string{"https://test.tau"},
	}
	wa, err := newWebAuthnStore(srv.db, defaults, func(accountID string) *memberStore {
		return newMemberStore(srv.db, accountID)
	})
	if err != nil {
		t.Fatalf("newWebAuthnStore: %v", err)
	}
	ctx := context.Background()
	if _, _, err := wa.FinishLogin(ctx, "ghost-id", nil); err == nil {
		t.Fatalf("FinishLogin should error for unknown session id")
	}
	if _, err := wa.FinishRegistration(ctx, "ghost-id", nil); err == nil {
		t.Fatalf("FinishRegistration should error for unknown session id")
	}
}

func TestWebAuthnStore_NotConfigured_FinishX(t *testing.T) {
	srv := newTestService(t)
	wa, _ := newWebAuthnStore(srv.db, accountsIface.WebAuthnDefaults{}, func(string) *memberStore {
		return newMemberStore(srv.db, "")
	})
	ctx := context.Background()
	if _, _, err := wa.FinishLogin(ctx, "x", nil); err == nil {
		t.Fatalf("FinishLogin should fail when not configured")
	}
	if _, err := wa.FinishRegistration(ctx, "x", nil); err == nil {
		t.Fatalf("FinishRegistration should fail when not configured")
	}
}

func TestSelectEmailSender(t *testing.T) {
	// SMTP configured → use SMTPSender regardless of devMode.
	cfg := accountsConfig{
		emailSMTPHost: "smtp.example.com", emailSMTPFrom: "noreply@x",
	}
	if _, err := selectEmailSender(cfg, false); err != nil {
		t.Fatalf("smtp configured should succeed: %v", err)
	}
	// SMTP unset + DevMode → stdout fallback.
	if _, err := selectEmailSender(accountsConfig{}, true); err != nil {
		t.Fatalf("dev-mode fallback should succeed: %v", err)
	}
	// SMTP unset + production → error so the operator notices before
	// users get stuck mid-login.
	if _, err := selectEmailSender(accountsConfig{}, false); err == nil {
		t.Fatalf("expected error when SMTP unset and not in DevMode")
	}
}

func TestParseSessionTTL(t *testing.T) {
	if parseSessionTTL("") != 0 {
		t.Fatalf("empty should be 0")
	}
	if parseSessionTTL("48h") != 48*time.Hour {
		t.Fatalf("48h parse failed")
	}
	defer func() {
		if recover() == nil {
			t.Fatalf("expected panic on bad duration")
		}
	}()
	_ = parseSessionTTL("not-a-duration")
}
