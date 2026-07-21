package accounts

import (
	"context"
	"errors"
	"testing"

	"github.com/ipfs/go-log/v2"
	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	mockkvdb "github.com/taubyte/tau/pkg/kvdb/mock"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// newTestKV constructs an in-memory KVDB for unit testing the stores.
func newTestKV(t *testing.T) kvdb.KVDB {
	t.Helper()
	factory := mockkvdb.New()
	kv, err := factory.New(log.Logger("test"), protocolCommon.Accounts, 1)
	if err != nil {
		t.Fatalf("mock kvdb: %v", err)
	}
	return kv
}

// newTestService wires an AccountsService backed by the mock KVDB so we can
// exercise the in-process Client. The service is intentionally not started
// (no node, stream, http) — the management surface only needs the KV.
//
// devMode is set to true so the operator-only verb gate is permissive:
// unit tests don't need to seed an OperatorToken for every account/plan
// management call. Tests that exercise the gate explicitly (operator_gate_test.go)
// override devMode and the configured token.
func newTestService(t *testing.T) *AccountsService {
	t.Helper()
	return &AccountsService{
		ctx:     context.Background(),
		db:      newTestKV(t),
		devMode: true,
	}
}

func TestAccountStore_CreateGetUpdateList(t *testing.T) {
	srv := newTestService(t)
	store := newAccountStore(srv.db)
	ctx := context.Background()

	// Create with required fields.
	acc, err := store.Create(ctx, accountsIface.CreateAccountInput{
		Slug: "acme",
		Name: "Acme Corp",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if acc.AuthMode != accountsIface.AuthModeManaged {
		t.Fatalf("default AuthMode = %q, want managed", acc.AuthMode)
	}
	if acc.Status != accountsIface.AccountStatusActive {
		t.Fatalf("default Status = %q, want active", acc.Status)
	}
	if acc.Kind != accountsIface.AccountKindOrg {
		t.Fatalf("default Kind = %q, want org", acc.Kind)
	}

	// Slug uniqueness.
	if _, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "duplicate"}); err == nil {
		t.Fatalf("expected duplicate-slug error")
	}

	// Get by ID and by slug.
	got, err := store.Get(ctx, acc.ID)
	if err != nil || got.Slug != "acme" {
		t.Fatalf("Get: %v %+v", err, got)
	}
	bySlug, err := store.GetBySlug(ctx, "acme")
	if err != nil || bySlug.ID != acc.ID {
		t.Fatalf("GetBySlug: %v %+v", err, bySlug)
	}

	// List.
	ids, err := store.List(ctx)
	if err != nil || len(ids) != 1 || ids[0] != acc.ID {
		t.Fatalf("List: %v %+v", err, ids)
	}

	// Update.
	newName := "Acme Inc."
	updated, err := store.Update(ctx, acc.ID, accountsIface.UpdateAccountInput{Name: &newName})
	if err != nil || updated.Name != newName {
		t.Fatalf("Update: %v %+v", err, updated)
	}

	// Delete + Get → ErrNotFound.
	if err := store.Delete(ctx, acc.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(ctx, acc.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
	if _, err := store.GetBySlug(ctx, "acme"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetBySlug after delete: %v", err)
	}
}

func TestAccountStore_SlugValidation(t *testing.T) {
	srv := newTestService(t)
	store := newAccountStore(srv.db)
	ctx := context.Background()

	bad := []string{"", "with space", "x!", "1leading", "trailing-", "kebab-case", "thisslugiswaytoolongforthelimitsetbythevalidatorwhichmaxesoutatsixtyfourchars_"}
	for _, slug := range bad {
		if _, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: slug, Name: "x"}); err == nil {
			t.Errorf("slug %q should have failed validation", slug)
		}
	}

	// Varname rules are case-sensitive: Acme and acme are distinct slugs.
	ok := []string{"acme", "Acme", "acme_dev", "team_1", "_under"}
	for _, slug := range ok {
		if _, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: slug, Name: "x"}); err != nil {
			t.Errorf("slug %q should be valid: %v", slug, err)
		}
	}
}

// TestVerify covers the community Verify path: a linked, active Account IS
// the access grant, and the summary carries no ee data.
func TestVerify(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	// Unknown git user → not linked.
	r, err := cli.Verify(ctx, "github", "doesnotexist")
	if err != nil || r.Linked {
		t.Fatalf("Verify unknown: %v %+v", err, r)
	}

	// Set up: Account + linked User.
	seedAccountUser(t, srv, "acme", "github", "42")

	// Verify → linked, with one Account.
	r, err = cli.Verify(ctx, "github", "42")
	if err != nil || !r.Linked {
		t.Fatalf("Verify linked: %v %+v", err, r)
	}
	if len(r.Accounts) != 1 || r.Accounts[0].Slug != "acme" {
		t.Fatalf("expected one Account 'acme', got %+v", r.Accounts)
	}
}

func TestMemberStore_InviteAndIndex(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	accStore := newAccountStore(srv.db)
	acc, _ := accStore.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	ms := newMemberStore(srv.db, acc.ID)

	m, err := ms.Invite(ctx, accountsIface.InviteMemberInput{
		PrimaryEmail: "alice@example.com",
		Role:         accountsIface.RoleOwner,
	})
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if m.PrimaryEmail != "alice@example.com" {
		t.Fatalf("email canonicalisation: %q", m.PrimaryEmail)
	}

	// Lookup index has the entry.
	idx, err := readMemberIndexByPrefix(ctx, srv.db, LookupEmailPrefix("alice@example.com"))
	if err != nil {
		t.Fatalf("read email index: %v", err)
	}
	if len(idx) != 1 || idx[0].MemberID != m.ID {
		t.Fatalf("index entry: %+v", idx)
	}

	// Remove the Member → index entry gone.
	if err := ms.Remove(ctx, m.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	idx, err = readMemberIndexByPrefix(ctx, srv.db, LookupEmailPrefix("alice@example.com"))
	if err != nil || len(idx) != 0 {
		t.Fatalf("index after Remove: idx=%+v err=%v", idx, err)
	}
}
