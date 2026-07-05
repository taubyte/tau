package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// Targeted no-tag coverage for pref.go. The store's Enable / SetDisplayName /
// List / LatestEvent / Events-filtering / validatePRefName edge cases are
// otherwise exercised only by the dream-tagged integration tests, which
// don't contribute to this package's no-tag coverage.

func newPRefStoreForTest(t *testing.T, srv *AccountsService, accountSlug string) (string, *prefStore) {
	t.Helper()
	acc, err := newAccountStore(srv.db).Create(context.Background(), accountsIface.CreateAccountInput{
		Slug: accountSlug, Name: accountSlug,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acc.ID, newPRefStore(srv.db, acc.ID, newPlanStore(srv.db))
}

func TestValidatePRefName_EdgeCases(t *testing.T) {
	good := []string{"pro", "Pro", "pref_1", "_under", "a", "A1", "x_y_z"}
	for _, n := range good {
		if err := validatePRefName(n); err != nil {
			t.Errorf("name %q should be valid: %v", n, err)
		}
	}
	bad := []string{"", "1leading", "with space", "kebab-name", "tail-", "with!bang", "with.dot",
		strings.Repeat("a", 65)}
	for _, n := range bad {
		if err := validatePRefName(n); err == nil {
			t.Errorf("name %q should be invalid", n)
		}
	}
}

func TestPRefStore_Create_Errors(t *testing.T) {
	srv := newTestService(t)
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()

	// Missing member_id.
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro"}); err == nil {
		t.Fatalf("expected error for empty member_id")
	}
	// Bad name.
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "bad-name", MemberID: "system:t"}); err == nil {
		t.Fatalf("expected error for invalid name")
	}
	// Happy path then duplicate.
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err == nil {
		t.Fatalf("expected error for duplicate name")
	}
}

func TestPRefStore_List(t *testing.T) {
	srv := newTestService(t)
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	for _, n := range []string{"pro", "free", "enterprise"} {
		if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: n, MemberID: "system:t"}); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}
	names, err := prefs.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("want 3 prefs, got %d (%v)", len(names), names)
	}
}

func TestPRefStore_SetDisplayName(t *testing.T) {
	srv := newTestService(t)
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	pref, _ := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", DisplayName: "Pro", MemberID: "system:t"})
	if pref.DisplayName != "Pro" {
		t.Fatalf("create DisplayName: %q", pref.DisplayName)
	}

	upd, err := prefs.SetDisplayName(ctx, "pro", "Production")
	if err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	if upd.DisplayName != "Production" {
		t.Fatalf("update DisplayName: %q", upd.DisplayName)
	}

	// Empty falls back to Name.
	upd, err = prefs.SetDisplayName(ctx, "pro", "")
	if err != nil {
		t.Fatalf("SetDisplayName empty: %v", err)
	}
	if upd.DisplayName != "pro" {
		t.Fatalf("empty DisplayName should default to Name; got %q", upd.DisplayName)
	}

	// Works on disabled PRef too — pure cosmetic, no status check.
	if _, err := prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	upd, err = prefs.SetDisplayName(ctx, "pro", "Pro (legacy)")
	if err != nil {
		t.Fatalf("SetDisplayName on disabled: %v", err)
	}
	if upd.DisplayName != "Pro (legacy)" {
		t.Fatalf("display name not updated on disabled pref")
	}
	if upd.Status != accountsIface.PRefStatusDisabled {
		t.Fatalf("Status should still be disabled after cosmetic update; got %s", upd.Status)
	}

	// Missing PRef.
	if _, err := prefs.SetDisplayName(ctx, "ghost", "Whatever"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing pref; got %v", err)
	}
}

func TestPRefStore_EnableDisableRoundTrip(t *testing.T) {
	srv := newTestService(t)
	plans := newPlanStore(srv.db)
	plan, _ := plans.Create(context.Background(), accountsIface.CreatePlanInput{Name: "P"})

	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Assign before disable: succeeds.
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: plan.ID, MemberID: "system:t"}); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	// Disable.
	if _, err := prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	g, _ := prefs.Get(ctx, "pro")
	if g.Status != accountsIface.PRefStatusDisabled {
		t.Fatalf("status after Disable: %s", g.Status)
	}

	// Assign on disabled is rejected.
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: plan.ID, MemberID: "system:t"}); err == nil {
		t.Fatalf("expected Assign-on-disabled to be rejected")
	}

	// Enable.
	if _, err := prefs.Enable(ctx, accountsIface.EnablePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	g, _ = prefs.Get(ctx, "pro")
	if g.Status != accountsIface.PRefStatusActive {
		t.Fatalf("status after Enable: %s", g.Status)
	}

	// Disable on missing PRef → error.
	if _, err := prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "ghost", MemberID: "system:t"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for Disable on missing pref; got %v", err)
	}
	if _, err := prefs.Enable(ctx, accountsIface.EnablePRefInput{Name: "ghost", MemberID: "system:t"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for Enable on missing pref; got %v", err)
	}
}

func TestPRefStore_Assign_Errors(t *testing.T) {
	srv := newTestService(t)
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Missing plan_id.
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", MemberID: "system:t"}); err == nil {
		t.Fatalf("expected error for empty plan_id")
	}
	// Plan doesn't exist locally → retryable error with hint.
	_, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: "ghost-plan-id", MemberID: "system:t"})
	if err == nil {
		t.Fatalf("expected error for unknown plan_id")
	}
	if !strings.Contains(err.Error(), "retry") {
		t.Fatalf("error should mention retry hint; got %v", err)
	}
	// Missing PRef.
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "ghost", PlanID: "x", MemberID: "system:t"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing pref; got %v", err)
	}
}

func TestPRefStore_Events_Filtering(t *testing.T) {
	srv := newTestService(t)
	plans := newPlanStore(srv.db)
	plan, _ := plans.Create(context.Background(), accountsIface.CreatePlanInput{Name: "P"})
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Three events spaced ≥1s apart so the cbor time encoding's sub-second
	// truncation can't collapse them onto the same stored timestamp.
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: plan.ID, MemberID: "system:t"}); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := prefs.Enable(ctx, accountsIface.EnablePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Enable: %v", err)
	}

	// Unbounded scan → all three. Use the stored At values (post-roundtrip)
	// as reference points so the filter bounds match what the store sees.
	all, err := prefs.Events(ctx, "pro", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("Events all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3 events, got %d", len(all))
	}
	tAssign, tDisable, tEnable := all[0].At, all[1].At, all[2].At
	if tAssign.Equal(tDisable) || tDisable.Equal(tEnable) {
		t.Fatalf("events not distinguishable post-roundtrip: %v %v %v", tAssign, tDisable, tEnable)
	}

	// from bound — exclude assign.
	mids, err := prefs.Events(ctx, "pro", tDisable, time.Time{})
	if err != nil {
		t.Fatalf("Events from-bounded: %v", err)
	}
	if len(mids) != 2 {
		t.Fatalf("from-bounded scan want 2, got %d", len(mids))
	}

	// to bound — exclude enable.
	cuts, err := prefs.Events(ctx, "pro", time.Time{}, tDisable)
	if err != nil {
		t.Fatalf("Events to-bounded: %v", err)
	}
	if len(cuts) != 2 {
		t.Fatalf("to-bounded scan want 2, got %d", len(cuts))
	}

	// Narrow window — only the disable event.
	narrows, err := prefs.Events(ctx, "pro", tDisable, tDisable)
	if err != nil {
		t.Fatalf("Events narrow: %v", err)
	}
	if len(narrows) != 1 {
		t.Fatalf("narrow window want 1, got %d", len(narrows))
	}
	if narrows[0].Kind != accountsIface.PRefEventKindDisable {
		t.Fatalf("narrow window kind = %s, want disable", narrows[0].Kind)
	}
}

func TestPRefStore_LatestEvent(t *testing.T) {
	srv := newTestService(t)
	plans := newPlanStore(srv.db)
	plan, _ := plans.Create(context.Background(), accountsIface.CreatePlanInput{Name: "P"})
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// No events yet → ErrNotFound.
	if _, err := prefs.LatestEvent(ctx, "pro"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty log should be ErrNotFound; got %v", err)
	}

	_, _ = prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: plan.ID, MemberID: "system:t"})
	time.Sleep(2 * time.Millisecond)
	_, _ = prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: "system:t"})

	latest, err := prefs.LatestEvent(ctx, "pro")
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if latest.Kind != accountsIface.PRefEventKindDisable {
		t.Fatalf("latest kind = %s, want disable", latest.Kind)
	}
}

// derivedStatus's walk-back loop: latest event is an assign; the function
// must skip past assigns to find the most recent enable/disable.
func TestPRefStore_DerivedStatus_WalksPastAssigns(t *testing.T) {
	srv := newTestService(t)
	plans := newPlanStore(srv.db)
	plan, _ := plans.Create(context.Background(), accountsIface.CreatePlanInput{Name: "P"})
	plan2, _ := plans.Create(context.Background(), accountsIface.CreatePlanInput{Name: "P2"})
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Sequence: Disable → Enable → Assign → Assign → Get
	// Latest event is the second assign; derivedStatus must skip both
	// assigns and report Active (latest enable wins).
	_, _ = prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: "system:t"})
	time.Sleep(time.Millisecond)
	_, _ = prefs.Enable(ctx, accountsIface.EnablePRefInput{Name: "pro", MemberID: "system:t"})
	time.Sleep(time.Millisecond)
	_, _ = prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: plan.ID, MemberID: "system:t"})
	time.Sleep(time.Millisecond)
	_, _ = prefs.Assign(ctx, accountsIface.AssignPRefInput{Name: "pro", PlanID: plan2.ID, MemberID: "system:t"})

	got, err := prefs.Get(ctx, "pro")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != accountsIface.PRefStatusActive {
		t.Fatalf("status with assigns-after-enable should be active; got %s", got.Status)
	}

	// Sequence ending in Disable → Get returns Disabled.
	_, _ = prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: "system:t"})
	got, err = prefs.Get(ctx, "pro")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != accountsIface.PRefStatusDisabled {
		t.Fatalf("status after Disable should be disabled; got %s", got.Status)
	}
}

func TestPRefStore_WriteEvent_RequiresMemberID(t *testing.T) {
	srv := newTestService(t)
	_, prefs := newPRefStoreForTest(t, srv, "acme")
	ctx := context.Background()
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{Name: "pro", MemberID: "system:t"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Disable with empty MemberID → writeEvent rejects.
	if _, err := prefs.Disable(ctx, accountsIface.DisablePRefInput{Name: "pro", MemberID: ""}); err == nil {
		t.Fatalf("expected error for empty member_id on Disable")
	}
}
