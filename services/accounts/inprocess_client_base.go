package accounts

import (
	"context"
	"errors"
	"strings"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

type inProcessClient struct {
	srv *AccountsService

	accounts *accountStore

	// eeSurface carries the ee-only methods — empty in the community build,
	// injected under -tags ee.
	eeSurface
}

// newBase wires the shared in-process client. newInProcessClient (build-tagged)
// wraps it: the community build returns it as-is, the ee build injects the ee
// surface.
func newBase(srv *AccountsService) *inProcessClient {
	return &inProcessClient{
		srv:      srv,
		accounts: newAccountStore(srv.db),
	}
}

// Verify reports which Accounts a git user is linked to. A linked, active
// account IS the access grant; the summary carries no extra data.
func (c *inProcessClient) Verify(ctx context.Context, provider, externalID string) (*accountsIface.VerifyResponse, error) {
	if provider == "" || externalID == "" {
		return nil, errors.New("accounts: provider and external_id required")
	}
	idx, err := c.readGitUserIndex(ctx, provider, externalID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.VerifyResponse{Linked: false}, nil
		}
		return nil, err
	}
	resp := &accountsIface.VerifyResponse{Linked: len(idx) > 0}
	for _, e := range idx {
		acc, err := c.accounts.Get(ctx, e.AccountID)
		if err != nil {
			continue // skip stale index entries
		}
		us := newUserStore(c.srv.db, e.AccountID)
		if _, err := us.Get(ctx, e.UserID); err != nil {
			continue // linkage gone
		}
		resp.Accounts = append(resp.Accounts, accountsIface.VerifyAccountSummary{
			ID:   acc.ID,
			Slug: acc.Slug,
			Name: acc.Name,
		})
	}
	if len(resp.Accounts) == 0 {
		resp.Linked = false
	}
	return resp, nil
}

// readGitUserIndex walks the git_user index across all accounts. userStore's
// helper is per-Account; the verify path needs cross-Account, hence this
// thin wrapper with an empty accountID.
func (c *inProcessClient) readGitUserIndex(ctx context.Context, provider, externalID string) ([]gitUserIndexEntry, error) {
	tmp := &userStore{db: c.srv.db, accountID: ""}
	return tmp.readGitUserIndex(ctx, provider, externalID)
}

func (c *inProcessClient) readMemberEmailIndex(ctx context.Context, email string) ([]memberIndexEntry, error) {
	return readMemberIndexByPrefix(ctx, c.srv.db, LookupEmailPrefix(email))
}

// LookupAccountsByEmail is a pure index lookup — no filtering on Account or
// Member status. Callers apply their own policy on the returned IDs.
func (c *inProcessClient) LookupAccountsByEmail(ctx context.Context, email string) ([]string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, errors.New("accounts: email required")
	}
	idx, err := c.readMemberEmailIndex(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return []string{}, nil
		}
		return nil, err
	}
	seen := make(map[string]struct{}, len(idx))
	out := make([]string, 0, len(idx))
	for _, e := range idx {
		if _, dup := seen[e.AccountID]; dup {
			continue
		}
		seen[e.AccountID] = struct{}{}
		out = append(out, e.AccountID)
	}
	return out, nil
}

func (c *inProcessClient) Accounts() accountsIface.Accounts {
	return c.accounts
}

func (c *inProcessClient) Members(accountID string) accountsIface.Members {
	return newMemberStore(c.srv.db, accountID)
}

func (c *inProcessClient) Users(accountID string) accountsIface.Users {
	return newUserStore(c.srv.db, accountID)
}

// Login falls back to a not-implemented stub when sessions aren't initialised
// (unit tests that don't go through service.New hit this path).
func (c *inProcessClient) Login() accountsIface.Login {
	if c.srv != nil && c.srv.sessions != nil {
		return &loginDispatcher{managed: newLoginManaged(c.srv), srv: c.srv}
	}
	return notImplementedLogin{}
}

func (c *inProcessClient) Peers(...peerCore.ID) accountsIface.Client { return c }
func (c *inProcessClient) Close()                                    {}

type notImplementedLogin struct{}

var errLoginNotImplemented = errors.New("accounts: login subsystem not initialised")

func (notImplementedLogin) StartManaged(context.Context, accountsIface.StartManagedLoginInput) (*accountsIface.ManagedLoginChallenge, error) {
	return nil, errLoginNotImplemented
}
func (notImplementedLogin) FinishManagedPasskey(context.Context, accountsIface.FinishPasskeyInput) (*accountsIface.Session, error) {
	return nil, errLoginNotImplemented
}
func (notImplementedLogin) FinishManagedMagicLink(context.Context, accountsIface.FinishMagicLinkInput) (*accountsIface.Session, error) {
	return nil, errLoginNotImplemented
}
func (notImplementedLogin) StartExternal(context.Context, string) (*accountsIface.ExternalLoginRedirect, error) {
	return nil, errLoginNotImplemented
}
func (notImplementedLogin) FinishExternal(context.Context, accountsIface.FinishExternalLoginInput) (*accountsIface.Session, error) {
	return nil, errLoginNotImplemented
}
func (notImplementedLogin) VerifySession(context.Context, string) (*accountsIface.Session, error) {
	return nil, errLoginNotImplemented
}
func (notImplementedLogin) Logout(context.Context, string) error { return errLoginNotImplemented }
