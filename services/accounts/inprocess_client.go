package accounts

import (
	"context"
	"errors"
	"strings"
	"time"

	peerCore "github.com/libp2p/go-libp2p/core/peer"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

type inProcessClient struct {
	srv *AccountsService

	accounts *accountStore
	plans    *planStore // global, account-agnostic
}

func newInProcessClient(srv *AccountsService) accountsIface.Client {
	return &inProcessClient{
		srv:      srv,
		accounts: newAccountStore(srv.db),
		plans:    newPlanStore(srv.db),
	}
}

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
		prefs := newPRefStore(c.srv.db, e.AccountID, c.plans)
		for _, g := range u.PlanGrants {
			pref, err := prefs.Get(ctx, g.PRefName)
			if err != nil {
				continue // grant references a PRef that's been removed
			}
			summary.PRefs = append(summary.PRefs, accountsIface.VerifyPRefSummary{
				Name:        pref.Name,
				DisplayName: pref.DisplayName,
				IsDefault:   g.IsDefault,
			})
		}
		resp.Accounts = append(resp.Accounts, summary)
	}
	if len(resp.Accounts) == 0 {
		resp.Linked = false
	}
	return resp, nil
}

// ResolvePRef returns Valid=true when (accountSlug, prefName) names an active
// PRef with a currently-assigned Plan and the caller's git user has a grant
// on the PRef. Otherwise Valid=false with a typed Reason from the set
// {account not found, account not active, pref not found, pref disabled,
// pref has no plan assigned, plan not found, git user not linked to account,
// git user has no grant on pref}.
func (c *inProcessClient) ResolvePRef(ctx context.Context, accountSlug, prefName, provider, externalID string) (*accountsIface.ResolveResponse, error) {
	acc, err := c.accounts.GetBySlug(ctx, accountSlug)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "account not found"}, nil
		}
		return nil, err
	}
	if acc.Status != accountsIface.AccountStatusActive {
		return &accountsIface.ResolveResponse{Valid: false, Reason: "account not active"}, nil
	}
	prefs := newPRefStore(c.srv.db, acc.ID, c.plans)
	pref, err := prefs.Get(ctx, prefName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "pref not found"}, nil
		}
		return nil, err
	}
	if pref.Status != accountsIface.PRefStatusActive {
		return &accountsIface.ResolveResponse{Valid: false, Reason: "pref disabled", PRef: pref}, nil
	}

	planID, err := latestAssignedPlanID(ctx, prefs, pref.Name)
	if err != nil {
		return nil, err
	}
	if planID == "" {
		return &accountsIface.ResolveResponse{Valid: false, Reason: "pref has no plan assigned", PRef: pref}, nil
	}
	plan, err := c.plans.Get(ctx, planID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "plan not found", PRef: pref}, nil
		}
		return nil, err
	}

	us := newUserStore(c.srv.db, acc.ID)
	u, err := us.GetByExternal(ctx, provider, externalID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return &accountsIface.ResolveResponse{Valid: false, Reason: "git user not linked to account", PRef: pref, Plan: plan}, nil
		}
		return nil, err
	}
	for _, g := range u.PlanGrants {
		if g.PRefName == pref.Name {
			return &accountsIface.ResolveResponse{Valid: true, PRef: pref, Plan: plan}, nil
		}
	}
	return &accountsIface.ResolveResponse{
		Valid:  false,
		Reason: "git user has no grant on pref",
		PRef:   pref,
		Plan:   plan,
	}, nil
}

// latestAssignedPlanID returns the PlanID of the most recent assign event, or
// "" when the log has no assigns. The PRef's "current plan" is defined as the
// latest assign — enable/disable events don't change it.
func latestAssignedPlanID(ctx context.Context, prefs *prefStore, prefName string) (string, error) {
	events, err := prefs.Events(ctx, prefName, time.Time{}, time.Time{})
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == accountsIface.PRefEventKindAssign {
			return events[i].PlanID, nil
		}
	}
	return "", nil
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

func (c *inProcessClient) Plans() accountsIface.Plans {
	return c.plans
}

func (c *inProcessClient) PRefs(accountID string) accountsIface.PRefs {
	return newPRefStore(c.srv.db, accountID, c.plans)
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
