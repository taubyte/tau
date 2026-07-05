//go:build dreaming

package accounts

import (
	"context"

	"github.com/taubyte/tau/services/accounts/email"
)

// Test-only helpers exposed to packages outside services/accounts under
// -tags=dreaming. Living here (not in *_test.go) lets the dream integration
// tests in services/accounts/tests/ import them.

// IssueTestSession bypasses magic-link/passkey to mint a Member-session
// bearer directly. Verifies as long as the per-Account signing key is reachable.
func (srv *AccountsService) IssueTestSession(ctx context.Context, accountID, memberID string) (string, error) {
	if srv.sessions == nil {
		// service.New normally wires sessions; this guards callers that
		// constructed AccountsService without going through New.
		srv.sessions = newSessionStore(srv.db, parseSessionTTL(""))
	}
	_, bearer, err := srv.sessions.Issue(ctx, accountID, memberID)
	return bearer, err
}

// SwapEmailSender returns the previous sender so tests can restore it.
func (srv *AccountsService) SwapEmailSender(captured email.Sender) email.Sender {
	if srv.magicLink == nil {
		return nil
	}
	prev := srv.magicLink.sender
	srv.magicLink.sender = captured
	return prev
}

func (srv *AccountsService) IssueTestSessionForMember(ctx context.Context, primaryEmail string) (bearer, accountID, memberID string, err error) {
	idx, err := readMemberIndexByPrefix(ctx, srv.db, LookupEmailPrefix(primaryEmail))
	if err != nil {
		return "", "", "", err
	}
	if len(idx) == 0 {
		return "", "", "", ErrNotFound
	}
	bearer, err = srv.IssueTestSession(ctx, idx[0].AccountID, idx[0].MemberID)
	return bearer, idx[0].AccountID, idx[0].MemberID, err
}
