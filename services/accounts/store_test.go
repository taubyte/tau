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

func TestPlanStore_CRUD(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	bs := newPlanStore(srv.db)

	plan, err := bs.Create(ctx, accountsIface.CreatePlanInput{
		Name: "Production",
	})
	if err != nil {
		t.Fatalf("Create plan: %v", err)
	}
	if plan.Name != "Production" {
		t.Fatalf("plan name mismatch: %+v", plan)
	}
	if plan.DisplayName != "Production" {
		t.Fatalf("DisplayName should default to Name; got %q", plan.DisplayName)
	}

	// Get.
	got, err := bs.Get(ctx, plan.ID)
	if err != nil || got.ID != plan.ID {
		t.Fatalf("Get: %v %+v", err, got)
	}

	// List.
	ids, err := bs.List(ctx)
	if err != nil || len(ids) != 1 {
		t.Fatalf("List: %v %+v", err, ids)
	}

	// Plans are immutable and undeletable — no Update, no Delete.
}

func TestUserStore_GrantsAndDefault(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	accStore := newAccountStore(srv.db)
	acc, _ := accStore.Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})

	// Two PRefs. Grants are PRef-name-keyed; the plan they point at is
	// irrelevant for the grant default semantics this test exercises.
	plans := newPlanStore(srv.db)
	plan, _ := plans.Create(ctx, accountsIface.CreatePlanInput{Name: "Prod"})
	prefs := newPRefStore(srv.db, acc.ID, plans)
	for _, name := range []string{"prod", "staging"} {
		if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: name, MemberID: "system:test"}); err != nil {
			t.Fatalf("Create pref %s: %v", name, err)
		}
		if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: name, PlanID: plan.ID, MemberID: "system:test"}); err != nil {
			t.Fatalf("Assign pref %s: %v", name, err)
		}
	}

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
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPRefInput{PRefName: "prod"}); err != nil {
		t.Fatalf("Grant prod: %v", err)
	}
	got, _ := us.Get(ctx, user.ID)
	if len(got.PlanGrants) != 1 || !got.PlanGrants[0].IsDefault {
		t.Fatalf("first grant should be default, got %+v", got.PlanGrants)
	}

	// Second grant non-default.
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPRefInput{PRefName: "staging"}); err != nil {
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
	if err := us.Grant(ctx, user.ID, accountsIface.GrantPRefInput{PRefName: "staging", IsDefault: true}); err != nil {
		t.Fatalf("Promote staging: %v", err)
	}
	got, _ = us.Get(ctx, user.ID)
	for _, g := range got.PlanGrants {
		if g.PRefName == "prod" && g.IsDefault {
			t.Fatalf("prod should be demoted")
		}
		if g.PRefName == "staging" && !g.IsDefault {
			t.Fatalf("staging should be default")
		}
	}

	// Revoke default → other grant promotes.
	if err := us.Revoke(ctx, user.ID, "staging"); err != nil {
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

	// 2) Set up: Account + Plan + PRef (assigned) + User + Grant.
	seedAccountUserPRef(t, srv, "acme", "prod", "github", "42")

	// 3) Verify → linked, with one Account and one PRef.
	r, err = cli.Verify(ctx, "github", "42")
	if err != nil || !r.Linked {
		t.Fatalf("Verify linked: %v %+v", err, r)
	}
	if len(r.Accounts) != 1 || r.Accounts[0].Slug != "acme" {
		t.Fatalf("expected one Account 'acme', got %+v", r.Accounts)
	}
	if len(r.Accounts[0].PRefs) != 1 || r.Accounts[0].PRefs[0].Name != "prod" {
		t.Fatalf("expected one pref 'prod', got %+v", r.Accounts[0].PRefs)
	}
	if !r.Accounts[0].PRefs[0].IsDefault {
		t.Fatalf("expected prod to be default grant")
	}

	// 4) ResolvePRef → valid for known grant.
	res, err := cli.ResolvePRef(ctx, "acme", "prod", "github", "42")
	if err != nil || !res.Valid {
		t.Fatalf("ResolvePRef valid: %v %+v", err, res)
	}

	// 5) ResolvePRef → invalid for unknown pref.
	res, _ = cli.ResolvePRef(ctx, "acme", "nope", "github", "42")
	if res.Valid {
		t.Fatalf("ResolvePRef nope: should be invalid")
	}
	if res.Reason != "pref not found" {
		t.Fatalf("ResolvePRef nope: bad reason %q", res.Reason)
	}

	// 6) ResolvePRef → invalid when User has no grant on the pref.
	// Create a second PRef with the same plan; user has no grant on it.
	acc, _ := newAccountStore(srv.db).GetBySlug(ctx, "acme")
	plan, _ := newPlanStore(srv.db).Create(ctx, accountsIface.CreatePlanInput{Name: "Staging"})
	prefs := newPRefStore(srv.db, acc.ID, newPlanStore(srv.db))
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "staging", MemberID: "system:test"}); err != nil {
		t.Fatalf("Create staging pref: %v", err)
	}
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "staging", PlanID: plan.ID, MemberID: "system:test"}); err != nil {
		t.Fatalf("Assign staging pref: %v", err)
	}
	res, _ = cli.ResolvePRef(ctx, "acme", "staging", "github", "42")
	if res.Valid {
		t.Fatalf("ResolvePRef without grant: should be invalid")
	}
	if res.Reason != "git user has no grant on pref" {
		t.Fatalf("bad reason %q", res.Reason)
	}

	// 7) ResolvePRef → invalid when account doesn't exist.
	res, _ = cli.ResolvePRef(ctx, "ghost", "prod", "github", "42")
	if res.Valid || res.Reason != "account not found" {
		t.Fatalf("ResolvePRef ghost: %+v", res)
	}

	// 8) ResolvePRef → invalid for disabled pref.
	if _, err := prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "prod", MemberID: "system:test"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	res, _ = cli.ResolvePRef(ctx, "acme", "prod", "github", "42")
	if res.Valid || res.Reason != "pref disabled" {
		t.Fatalf("ResolvePRef disabled: %+v", res)
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
