package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-webauthn/webauthn/protocol"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// loginManaged is the dispatcher for managed-mode authentication: passkey
// (WebAuthn) when the resolved Member has one registered, magic-link email
// otherwise.
//
// External-mode logins (Okta, OIDC, SAML) route through login_external.go
// (`!ee` stub) or login_external_ee.go (`ee` real impl).
type loginManaged struct {
	srv *AccountsService
}

func newLoginManaged(srv *AccountsService) *loginManaged {
	return &loginManaged{srv: srv}
}

// StartManaged kicks off a managed-mode login. The caller provides either an
// email (resolves to one or more Account/Member pairs) or an explicit
// account-slug (must be paired with the email at the level above; this v1
// shape returns a candidate list when ambiguous).
func (l *loginManaged) StartManaged(ctx context.Context, in accountsIface.StartManagedLoginInput) (*accountsIface.ManagedLoginChallenge, error) {
	if in.Email == "" && in.AccountSlug == "" {
		return nil, errors.New("accounts: email or account_slug required")
	}

	candidates, err := l.resolveCandidates(ctx, in)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.New("accounts: no member found for those credentials")
	}
	if len(candidates) > 1 {
		// Caller must pick one. Return the candidate list; subsequent
		// StartManaged with AccountSlug + Email narrows.
		return &accountsIface.ManagedLoginChallenge{Candidates: summarizeCandidates(candidates)}, nil
	}
	chosen := candidates[0]

	// Decide passkey vs magic-link.
	hasPasskey := len(chosen.member.PasskeyCredentials) > 0 && l.srv.webAuthn != nil && l.srv.webAuthn.Available()
	if hasPasskey {
		sessionID, options, err := l.srv.webAuthn.BeginLogin(ctx, chosen.account.ID, chosen.member.ID)
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(options)
		return &accountsIface.ManagedLoginChallenge{
			SessionID:         sessionID,
			WebAuthnChallenge: raw,
		}, nil
	}

	// Fallback to magic-link.
	if l.srv.magicLink == nil {
		return nil, errors.New("accounts: magic-link sender not configured")
	}
	if err := l.srv.magicLink.SendMagicLink(ctx, chosen.account.ID, chosen.member.ID, chosen.member.PrimaryEmail, ""); err != nil {
		return nil, err
	}
	return &accountsIface.ManagedLoginChallenge{MagicLinkSent: true}, nil
}

// FinishManagedPasskey verifies a WebAuthn assertion previously challenged by
// StartManaged. On success returns a fresh Member session.
func (l *loginManaged) FinishManagedPasskey(ctx context.Context, in accountsIface.FinishPasskeyInput) (*accountsIface.Session, error) {
	if in.SessionID == "" {
		return nil, errors.New("accounts: session_id required")
	}
	if l.srv.webAuthn == nil || !l.srv.webAuthn.Available() {
		return nil, errors.New("accounts: webauthn not configured")
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(in.Assertion))
	if err != nil {
		return nil, fmt.Errorf("accounts: parse webauthn assertion: %w", err)
	}
	accountID, memberID, err := l.srv.webAuthn.FinishLogin(ctx, in.SessionID, parsed)
	if err != nil {
		return nil, err
	}
	return l.issueSession(ctx, accountID, memberID)
}

// FinishManagedMagicLink verifies a magic-link code and returns a session.
func (l *loginManaged) FinishManagedMagicLink(ctx context.Context, in accountsIface.FinishMagicLinkInput) (*accountsIface.Session, error) {
	if l.srv.magicLink == nil {
		return nil, errors.New("accounts: magic-link sender not configured")
	}
	accountID, memberID, err := l.srv.magicLink.VerifyMagicLink(ctx, in.Code, in.ClientIP)
	if err != nil {
		return nil, err
	}
	return l.issueSession(ctx, accountID, memberID)
}

// VerifySession is the cookie / bearer-token verification entry point.
func (l *loginManaged) VerifySession(ctx context.Context, token string) (*accountsIface.Session, error) {
	if l.srv.sessions == nil {
		return nil, errors.New("accounts: session store unavailable")
	}
	accountID, memberID, err := l.srv.sessions.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	return &accountsIface.Session{
		AccountID: accountID,
		MemberID:  memberID,
		Token:     token,
	}, nil
}

// Logout revokes the session bearer.
func (l *loginManaged) Logout(ctx context.Context, token string) error {
	if l.srv.sessions == nil {
		return errors.New("accounts: session store unavailable")
	}
	return l.srv.sessions.Revoke(ctx, token)
}

func (l *loginManaged) issueSession(ctx context.Context, accountID, memberID string) (*accountsIface.Session, error) {
	if l.srv.sessions == nil {
		return nil, errors.New("accounts: session store unavailable")
	}
	sess, _, err := l.srv.sessions.Issue(ctx, accountID, memberID)
	return sess, err
}

// loginCandidate pairs a resolved Member with its Account (loaded from KV).
type loginCandidate struct {
	account *accountsIface.Account
	member  *accountsIface.Member
}

// resolveCandidates walks the email or account-slug indexes to find matching
// (account, member) pairs.
func (l *loginManaged) resolveCandidates(ctx context.Context, in accountsIface.StartManagedLoginInput) ([]loginCandidate, error) {
	switch {
	case in.AccountSlug != "" && in.Email == "":
		acc, err := newAccountStore(l.srv.db).GetBySlug(ctx, in.AccountSlug)
		if err != nil {
			return nil, err
		}
		return l.candidatesFromAccount(ctx, acc, "")
	case in.AccountSlug != "" && in.Email != "":
		acc, err := newAccountStore(l.srv.db).GetBySlug(ctx, in.AccountSlug)
		if err != nil {
			return nil, err
		}
		return l.candidatesFromAccount(ctx, acc, in.Email)
	default:
		return l.candidatesFromEmail(ctx, in.Email)
	}
}

func (l *loginManaged) candidatesFromEmail(ctx context.Context, emailStr string) ([]loginCandidate, error) {
	idx, err := newMemberStore(l.srv.db, "").readMemberIndex(ctx, LookupEmailPath(emailStr))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]loginCandidate, 0, len(idx))
	for _, e := range idx {
		acc, err := newAccountStore(l.srv.db).Get(ctx, e.AccountID)
		if err != nil {
			continue
		}
		m, err := newMemberStore(l.srv.db, e.AccountID).Get(ctx, e.MemberID)
		if err != nil {
			continue
		}
		out = append(out, loginCandidate{account: acc, member: m})
	}
	return out, nil
}

func (l *loginManaged) candidatesFromAccount(ctx context.Context, acc *accountsIface.Account, emailFilter string) ([]loginCandidate, error) {
	ms := newMemberStore(l.srv.db, acc.ID)
	ids, err := ms.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]loginCandidate, 0, len(ids))
	for _, id := range ids {
		m, err := ms.Get(ctx, id)
		if err != nil {
			continue
		}
		if emailFilter != "" && m.PrimaryEmail != hashLowerSafe(emailFilter) && m.PrimaryEmail != emailFilter {
			continue
		}
		out = append(out, loginCandidate{account: acc, member: m})
	}
	return out, nil
}

// hashLowerSafe is the same lowercased-trimmed form Member.Invite uses on
// PrimaryEmail; resolving by email needs the same canonicalisation.
func hashLowerSafe(s string) string {
	// Members store the lowercased+trimmed plaintext email, not its hash.
	// (The hash is only the index key.) Mirror the canonicalisation here.
	return canonicalEmail(s)
}

func canonicalEmail(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

// summarizeCandidates produces a public VerifyAccountSummary list — used by
// StartManaged to let the caller pick when an email maps to multiple accounts.
func summarizeCandidates(in []loginCandidate) []accountsIface.VerifyAccountSummary {
	out := make([]accountsIface.VerifyAccountSummary, 0, len(in))
	for _, c := range in {
		out = append(out, accountsIface.VerifyAccountSummary{
			ID: c.account.ID, Slug: c.account.Slug, Name: c.account.Name,
		})
	}
	return out
}
