//go:build dreaming

package accounts

import (
	"context"

	"github.com/taubyte/tau/services/accounts/email"
)

// Test-only helpers compiled in only under `-tags=dreaming`. They expose
// internals (session issuance, captured email sender) so the dream-mode
// integration tests in services/accounts/tests/ can exercise the full
// HTTP / wire stack without forcing the magic-link side-channel.
//
// These intentionally live here (not in *_test.go) so packages outside
// services/accounts can import them when running under -tags=dreaming.

// IssueTestSession mints a Member-session bearer for the given (account,
// member) without going through magic-link or passkey. The bearer verifies
// as long as the per-Account signing key is reachable.
func (srv *AccountsService) IssueTestSession(ctx context.Context, accountID, memberID string) (string, error) {
	if srv.sessions == nil {
		// In dream tests the auth subsystems are wired by service.New, so
		// this branch only fires if the caller constructed an AccountsService
		// without going through New. Guarded for safety.
		srv.sessions = newSessionStore(srv.db, parseSessionTTL(""))
	}
	_, bearer, err := srv.sessions.Issue(ctx, accountID, memberID)
	return bearer, err
}

// SwapEmailSender installs a captured sender on the running service so dream
// tests can read what the magic-link flow emails out. Returns the sender
// previously in use (typically a stdout sender) so tests can restore it.
//
// The returned StdoutSender's Sent() slice grows with every outbound email
// — tests use that to extract the magic-link code from a real round-trip.
func (srv *AccountsService) SwapEmailSender(captured email.Sender) email.Sender {
	if srv.magicLink == nil {
		return nil
	}
	prev := srv.magicLink.sender
	srv.magicLink.sender = captured
	return prev
}

// IssueTestSessionForMember is a convenience that mints a session for an
// already-created Member identified by primary email. Returns the bearer
// and the resolved (account_id, member_id).
func (srv *AccountsService) IssueTestSessionForMember(ctx context.Context, primaryEmail string) (bearer, accountID, memberID string, err error) {
	tmp := &memberStore{db: srv.db}
	idx, err := tmp.readMemberIndex(ctx, LookupEmailPath(primaryEmail))
	if err != nil {
		return "", "", "", err
	}
	if len(idx) == 0 {
		return "", "", "", ErrNotFound
	}
	bearer, err = srv.IssueTestSession(ctx, idx[0].AccountID, idx[0].MemberID)
	return bearer, idx[0].AccountID, idx[0].MemberID, err
}
