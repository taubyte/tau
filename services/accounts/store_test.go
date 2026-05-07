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

	bad := []string{"", "Acme", "with space", "x!", "-leading", "trailing-", "thisslugiswaytoolongforthelimitsetbythevalidatorwhichmaxesoutatsixtyfourchars-"}
	for _, slug := range bad {
		if _, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: slug, Name: "x"}); err == nil {
			t.Errorf("slug %q should have failed validation", slug)
		}
	}

	ok := []string{"acme", "acme-corp", "acme_dev", "team-1"}
	for _, slug := range ok {
		if _, err := store.Create(ctx, accountsIface.CreateAccountInput{Slug: slug, Name: "x"}); err != nil {
			t.Errorf("slug %q should be valid: %v", slug, err)
		}
	}
}

func TestPlanStore_CRUD(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	accStore := newAccountStore(srv.db)
	acc, err := accStore.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	if err != nil {
		t.Fatalf("Create account: %v", err)
	}
	bs := newPlanStore(srv.db, acc.ID)

	plan, err := bs.Create(ctx, accountsIface.CreatePlanInput{
		Slug: "prod",
		Name: "Production",
		Mode: accountsIface.PlanModeQuota,
	})
	if err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	if plan.AccountID != acc.ID {
		t.Fatalf("plan account_id mismatch")
	}

	// GetBySlug.
	bySlug, err := bs.GetBySlug(ctx, "prod")
	if err != nil || bySlug.ID != plan.ID {
		t.Fatalf("GetBySlug: %v %+v", err, bySlug)
	}

	// List.
	ids, err := bs.List(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("List: %v %+v", err, ids)
	}

	// Update + Delete.
	suspended := accountsIface.PlanStatusSuspended
	if _, err := bs.Update(ctx, plan.ID, accountsIface.UpdatePlanInput{Status: &suspended}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := bs.Delete(ctx, plan.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := bs.Get(ctx, plan.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
}

func TestUserStore_GrantsAndDefault(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	accStore := newAccountStore(srv.db)
	acc, _ := accStore.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bs := newPlanStore(srv.db, acc.ID)
	prod, _ := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})
	stg, _ := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "staging", Name: "Staging"})

	us := newUserStore(srv.db, acc.ID)
	user, err := us.Add(ctx, accountsIface.AddUserInput{
		Provider:    "github",
		ExternalID:  "12345",
		DisplayName: "alice",
	})
	if err != nil {
		t.Fatalf("Add user: %v", err)
	}

	// First grant becomes default automatically.
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: prod.ID}); err != nil {
		t.Fatalf("Grant prod: %v", err)
	}
	got, _ := us.Get(ctx, user.ID)
	if len(got.PlanGrants) != 1 || !got.PlanGrants[0].IsDefault {
		t.Fatalf("first grant should be default, got %+v", got.PlanGrants)
	}

	// Second grant non-default.
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: stg.ID}); err != nil {
		t.Fatalf("Grant staging: %v", err)
	}
	got, _ = us.Get(ctx, user.ID)
	defaults := 0
	for _, g := range got.PlanGrants {
		if g.IsDefault {
			defaults++
		}
	}
	if len(got.PlanGrants) != 2 || defaults != 1 {
		t.Fatalf("want 2 grants with exactly 1 default, got %+v", got.PlanGrants)
	}

	// Promote staging to default → prod is demoted.
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: stg.ID, IsDefault: true}); err != nil {
		t.Fatalf("Promote staging: %v", err)
	}
	got, _ = us.Get(ctx, user.ID)
	for _, g := range got.PlanGrants {
		if g.PlanID == prod.ID && g.IsDefault {
			t.Fatalf("prod should be demoted")
		}
		if g.PlanID == stg.ID && !g.IsDefault {
			t.Fatalf("staging should be default")
		}
	}

	// Revoke default → other grant promotes.
	if err := us.Revoke(ctx, user.ID, stg.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, _ = us.Get(ctx, user.ID)
	if len(got.PlanGrants) != 1 {
		t.Fatalf("expected 1 grant after revoke, got %+v", got.PlanGrants)
	}
	if !got.PlanGrants[0].IsDefault {
		t.Fatalf("remaining grant should be promoted to default")
	}

	// GetByExternal returns the User.
	again, err := us.GetByExternal(ctx, "github", "12345")
	if err != nil || again.ID != user.ID {
		t.Fatalf("GetByExternal: %v %+v", err, again)
	}

	// Adding the same provider+external_id twice fails.
	if _, err := us.Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "12345"}); err == nil {
		t.Fatalf("duplicate User should fail")
	}

	// Remove user → git_user index cleaned.
	if err := us.Remove(ctx, user.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := us.GetByExternal(ctx, "github", "12345"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetByExternal after Remove: %v", err)
	}
}

func TestVerifyAndResolve(t *testing.T) {
	srv := newTestService(t)
	cli := newInProcessClient(srv)
	ctx := context.Background()

	// 1) Unknown git user → not linked.
	r, err := cli.Verify(ctx, "github", "doesnotexist")
	if err != nil || r.Linked {
		t.Fatalf("Verify unknown: %v %+v", err, r)
	}

	// 2) Set up: Account + Plan + User + Grant.
	accStore := newAccountStore(srv.db)
	acc, _ := accStore.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bs := newPlanStore(srv.db, acc.ID)
	prod, _ := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})
	us := newUserStore(srv.db, acc.ID)
	user, _ := us.Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "42", DisplayName: "alice"})
	_ = user
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: prod.ID}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// 3) Verify → linked, with one Account and one Plan.
	r, err = cli.Verify(ctx, "github", "42")
	if err != nil || !r.Linked {
		t.Fatalf("Verify linked: %v %+v", err, r)
	}
	if len(r.Accounts) != 1 || r.Accounts[0].Slug != "acme" {
		t.Fatalf("expected one Account 'acme', got %+v", r.Accounts)
	}
	if len(r.Accounts[0].Plans) != 1 || r.Accounts[0].Plans[0].Slug != "prod" {
		t.Fatalf("expected one plan 'prod', got %+v", r.Accounts[0].Plans)
	}
	if !r.Accounts[0].Plans[0].IsDefault {
		t.Fatalf("expected prod to be default grant")
	}

	// 4) ResolvePlan → valid for known grant.
	res, err := cli.ResolvePlan(ctx, "acme", "prod", "github", "42")
	if err != nil || !res.Valid {
		t.Fatalf("ResolvePlan valid: %v %+v", err, res)
	}

	// 5) ResolvePlan → invalid for unknown plan.
	res, _ = cli.ResolvePlan(ctx, "acme", "nope", "github", "42")
	if res.Valid {
		t.Fatalf("ResolvePlan nope: should be invalid")
	}
	if res.Reason != "plan not found" {
		t.Fatalf("ResolvePlan nope: bad reason %q", res.Reason)
	}

	// 6) ResolvePlan → invalid when User has no grant on the plan.
	other, _ := bs.Create(ctx, accountsIface.CreatePlanInput{Slug: "staging", Name: "Stg"})
	_ = other
	res, _ = cli.ResolvePlan(ctx, "acme", "staging", "github", "42")
	if res.Valid {
		t.Fatalf("ResolvePlan without grant: should be invalid")
	}
	if res.Reason != "git user has no grant on plan" {
		t.Fatalf("bad reason %q", res.Reason)
	}

	// 7) ResolvePlan → invalid when account doesn't exist.
	res, _ = cli.ResolvePlan(ctx, "ghost", "prod", "github", "42")
	if res.Valid || res.Reason != "account not found" {
		t.Fatalf("ResolvePlan ghost: %+v", res)
	}

	// 8) ResolvePlan → invalid for suspended plan.
	suspended := accountsIface.PlanStatusSuspended
	if _, err := bs.Update(ctx, prod.ID, accountsIface.UpdatePlanInput{Status: &suspended}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	res, _ = cli.ResolvePlan(ctx, "acme", "prod", "github", "42")
	if res.Valid || res.Reason != "plan suspended" {
		t.Fatalf("ResolvePlan suspended: %+v", res)
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
	idx, err := ms.readMemberIndex(ctx, LookupEmailPath("alice@example.com"))
	if err != nil {
		t.Fatalf("readMemberIndex: %v", err)
	}
	if len(idx) != 1 || idx[0].MemberID != m.ID {
		t.Fatalf("index entry: %+v", idx)
	}

	// Remove the Member → index entry gone.
	if err := ms.Remove(ctx, m.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := ms.readMemberIndex(ctx, LookupEmailPath("alice@example.com")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("index after Remove: %v", err)
	}
}
