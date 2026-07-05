package accounts

import (
	"context"
	"sort"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

// LookupAccountsByEmail is a pure email-index lookup. These tests cover the
// behavior contract spelled out in the design note: empty-input error, empty
// slice on no matches, multi-account fan-out, no-status-filtering (suspended
// and pending-claim are both included), case-insensitive matching, dedup, and
// post-Remove invisibility.

// inviteWithEmail invites a Member with the given email on the given Account.
func inviteWithEmail(t *testing.T, srv *AccountsService, accountID, email string) *accountsIface.Member {
	t.Helper()
	m, err := newMemberStore(srv.db, accountID).Invite(context.Background(), accountsIface.InviteMemberInput{
		PrimaryEmail: email,
		Role:         accountsIface.RoleOwner,
	})
	if err != nil {
		t.Fatalf("invite %s: %v", email, err)
	}
	return m
}

func TestLookupAccountsByEmail_EmptyInput(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	if _, err := cli.LookupAccountsByEmail(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty email")
	}
	if _, err := cli.LookupAccountsByEmail(context.Background(), "   "); err == nil {
		t.Fatalf("expected error for whitespace-only email")
	}
}

func TestLookupAccountsByEmail_NoMatches(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	ids, err := cli.LookupAccountsByEmail(context.Background(), "nobody@example.com")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail: %v", err)
	}
	if ids == nil {
		t.Fatalf("expected empty slice, got nil")
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 ids, got %d (%v)", len(ids), ids)
	}
}

func TestLookupAccountsByEmail_SingleAccount(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	inviteWithEmail(t, srv, acc.ID, "alice@example.com")

	cli := newInProcessClient(srv)
	ids, err := cli.LookupAccountsByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail: %v", err)
	}
	if len(ids) != 1 || ids[0] != acc.ID {
		t.Fatalf("expected [%s], got %v", acc.ID, ids)
	}
}

func TestLookupAccountsByEmail_MultipleAccounts(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	cli := newInProcessClient(srv)

	accA, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "alpha", Name: "Alpha"})
	accB, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "beta", Name: "Beta"})
	accC, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "gamma", Name: "Gamma"})

	// alice is on alpha and beta; not on gamma.
	inviteWithEmail(t, srv, accA.ID, "alice@x")
	inviteWithEmail(t, srv, accB.ID, "alice@x")
	inviteWithEmail(t, srv, accC.ID, "bob@x")

	ids, err := cli.LookupAccountsByEmail(ctx, "alice@x")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %d (%v)", len(ids), ids)
	}
	sort.Strings(ids)
	expected := []string{accA.ID, accB.ID}
	sort.Strings(expected)
	for i := range expected {
		if ids[i] != expected[i] {
			t.Fatalf("ids[%d] = %s, want %s", i, ids[i], expected[i])
		}
	}
}

func TestLookupAccountsByEmail_CaseInsensitive(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	// Member.Invite already normalizes the email to lower+trim on store.
	inviteWithEmail(t, srv, acc.ID, "alice@example.com")

	cli := newInProcessClient(srv)
	// Mixed-case lookup matches the lowercase-stored record.
	ids, err := cli.LookupAccountsByEmail(ctx, "Alice@Example.COM")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail: %v", err)
	}
	if len(ids) != 1 || ids[0] != acc.ID {
		t.Fatalf("case-insensitive: expected [%s], got %v", acc.ID, ids)
	}
	// Whitespace-padded lookup also matches.
	ids, err = cli.LookupAccountsByEmail(ctx, "  alice@example.com  ")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail (padded): %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("padded lookup: expected 1 id, got %v", ids)
	}
}

func TestLookupAccountsByEmail_IncludesSuspendedAccount(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	store := newAccountStore(srv.db)

	acc, _ := store.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	inviteWithEmail(t, srv, acc.ID, "alice@x")

	// Suspend the Account.
	suspended := accountsIface.AccountStatusSuspended
	if _, err := store.Update(ctx, acc.ID, accountsIface.UpdateAccountInput{Status: &suspended}); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	cli := newInProcessClient(srv)
	ids, err := cli.LookupAccountsByEmail(ctx, "alice@x")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail: %v", err)
	}
	// No policy at the lookup layer: suspended Accounts are still returned.
	if len(ids) != 1 || ids[0] != acc.ID {
		t.Fatalf("expected suspended Account included; got %v", ids)
	}
}

func TestLookupAccountsByEmail_IncludesPendingClaim(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})

	// Invite a Member; default Status is "pending-claim" until the invitee
	// claims (the existing Member.Invite path stamps "pending-claim").
	inviteWithEmail(t, srv, acc.ID, "alice@x")

	cli := newInProcessClient(srv)
	ids, err := cli.LookupAccountsByEmail(ctx, "alice@x")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail: %v", err)
	}
	// No policy at the lookup layer: pending-claim Members are returned.
	if len(ids) != 1 || ids[0] != acc.ID {
		t.Fatalf("expected pending-claim Member's Account included; got %v", ids)
	}
}

func TestLookupAccountsByEmail_AfterRemove(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	ms := newMemberStore(srv.db, acc.ID)
	m := inviteWithEmail(t, srv, acc.ID, "alice@x")

	cli := newInProcessClient(srv)
	if ids, _ := cli.LookupAccountsByEmail(ctx, "alice@x"); len(ids) != 1 {
		t.Fatalf("setup: expected 1 id, got %v", ids)
	}

	if err := ms.Remove(ctx, m.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	ids, err := cli.LookupAccountsByEmail(ctx, "alice@x")
	if err != nil {
		t.Fatalf("LookupAccountsByEmail after Remove: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected 0 ids after Remove, got %v", ids)
	}
}

// TestApiLookupAccountsByEmailHandler exercises the wire-level stream verb:
// happy path returns ids; missing email returns error.
func TestApiLookupAccountsByEmailHandler(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	inviteWithEmail(t, srv, acc.ID, "alice@x")

	// Happy.
	resp, err := srv.apiLookupAccountsByEmailHandler(ctx, nil, command.Body{"email": "alice@x"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	rawIDs, ok := resp["account_ids"]
	if !ok {
		t.Fatalf("response missing account_ids: %+v", resp)
	}
	switch ids := rawIDs.(type) {
	case []string:
		if len(ids) != 1 || ids[0] != acc.ID {
			t.Fatalf("ids = %v, want [%s]", ids, acc.ID)
		}
	default:
		t.Fatalf("account_ids has unexpected type %T", rawIDs)
	}

	// Missing email → error.
	if _, err := srv.apiLookupAccountsByEmailHandler(ctx, nil, command.Body{}); err == nil {
		t.Fatalf("expected error for missing email")
	}
}
