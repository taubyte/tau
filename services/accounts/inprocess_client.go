package accounts

import (
	"context"
	"errors"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// inProcessClient is the in-process Client returned by AccountsService.Client().
type inProcessClient struct {
	srv *AccountsService

	accounts *accountStore
}

func newInProcessClient(srv *AccountsService) accountsIface.Client {
	return &inProcessClient{
		srv:      srv,
		accounts: newAccountStore(srv.db),
	}
}

// --- Integration surface (verify + plan-resolve) ----------------

// Verify checks whether a git provider account is linked to ≥1 Account.
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
		u, err := us.Get(ctx, e.UserID)
		if err != nil {
			continue
		}
		summary := accountsIface.VerifyAccountSummary{
			ID:   acc.ID,
			Slug: acc.Slug,
			Name: acc.Name,
		}
		bs := newPlanStore(c.srv.db, e.AccountID)
		for _, g := range u.PlanGrants {
			b, err := bs.Get(ctx, g.PlanID)
			if err != nil {
				continue
			}
			summary.Plans = append(summary.Plans, accountsIface.VerifyPlanSummary{
				ID:        b.ID,
				Slug:      b.Slug,
				IsDefault: g.IsDefault,
			})
		}
		resp.Accounts = append(resp.Accounts, summary)
	}
	if len(resp.Accounts) == 0 {
		resp.Linked = false
	}
	return resp, nil
}

// ResolvePlan validates that (accountSlug, planSlug) names an active Plan
// the calling git user has a grant on.
func (c *inProcessClient) ResolvePlan(ctx context.Context, accountSlug, planSlug, provider, externalID string) (*accountsIface.ResolveResponse, error) {
	acc, err := c.accounts.GetBySlug(ctx, accountSlug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "account not found"}, nil
		}
		return nil, err
	}
	bs := newPlanStore(c.srv.db, acc.ID)
	plan, err := bs.GetBySlug(ctx, planSlug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "plan not found"}, nil
		}
		return nil, err
	}
	if plan.Status != accountsIface.PlanStatusActive {
		return &accountsIface.ResolveResponse{
			Valid:  false,
			Reason: "plan suspended",
			Plan:   plan,
		}, nil
	}
	us := newUserStore(c.srv.db, acc.ID)
	u, err := us.GetByExternal(ctx, provider, externalID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "git user not linked to account"}, nil
		}
		return nil, err
	}
	for _, g := range u.PlanGrants {
		if g.PlanID == plan.ID {
			return &accountsIface.ResolveResponse{Valid: true, Plan: plan}, nil
		}
	}
	return &accountsIface.ResolveResponse{
		Valid:  false,
		Reason: "git user has no grant on plan",
		Plan:   plan,
	}, nil
}

// readGitUserIndex is a client-side helper to walk the git_user index.
// Mirrors the helper on userStore (which is per-Account) — the verify path
// needs it across all Accounts, hence the duplication.
func (c *inProcessClient) readGitUserIndex(ctx context.Context, provider, externalID string) ([]gitUserIndexEntry, error) {
	tmp := &userStore{db: c.srv.db, accountID: ""}
	return tmp.readGitUserIndex(ctx, provider, externalID)
}

// --- Management surface --------------------------------------------

func (c *inProcessClient) Accounts() accountsIface.Accounts {
	return c.accounts
}

func (c *inProcessClient) Members(accountID string) accountsIface.Members {
	return newMemberStore(c.srv.db, accountID)
}

func (c *inProcessClient) Users(accountID string) accountsIface.Users {
	return newUserStore(c.srv.db, accountID)
}

func (c *inProcessClient) Plans(accountID string) accountsIface.Plans {
	return newPlanStore(c.srv.db, accountID)
}

// Login dispatches to the managed (passkey + magic-link) impl when the
// service has those subsystems initialised; otherwise falls back to a
// not-implemented stub (used by unit tests that don't go through service.New).
func (c *inProcessClient) Login() accountsIface.Login {
	if c.srv != nil && c.srv.sessions != nil {
		return &loginDispatcher{managed: newLoginManaged(c.srv), srv: c.srv}
	}
	return notImplementedLogin{}
}

func (c *inProcessClient) Peers(...peerCore.ID) accountsIface.Client { return c }
func (c *inProcessClient) Close()                                    {}

// --- not-implemented Login stub ------------------------------------

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
