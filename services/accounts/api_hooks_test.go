package accounts

import (
	"context"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

// Tests for the wire codec (verify/resolve response encoding + decoding) and
// the stream-handler dispatch layer added in Phase 3. These exercise the same
// shape used by clients/p2p/accounts and services/accounts in production.

func TestVerifyResponseRoundTrip(t *testing.T) {
	in := &accountsIface.VerifyResponse{
		Linked: true,
		Accounts: []accountsIface.VerifyAccountSummary{
			{ID: "a-1", Slug: "acme", Name: "Acme",
				Plans: []accountsIface.VerifyPlanSummary{{ID: "b-1", Slug: "prod", IsDefault: true}}},
		},
	}
	wire := verifyResponseToWire(in)
	out, err := VerifyResponseFromWire(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Linked != true || len(out.Accounts) != 1 || out.Accounts[0].Slug != "acme" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if len(out.Accounts[0].Plans) != 1 || out.Accounts[0].Plans[0].Slug != "prod" {
		t.Fatalf("plan round-trip mismatch: %+v", out.Accounts[0].Plans)
	}
}

func TestVerifyResponseToWire_NilAndEmpty(t *testing.T) {
	// Nil response → wire indicates not-linked.
	w := verifyResponseToWire(nil)
	if v, _ := w["linked"].(bool); v {
		t.Fatalf("nil should be not-linked")
	}
	// Linked=false: wire omits accounts list.
	w = verifyResponseToWire(&accountsIface.VerifyResponse{Linked: false})
	if _, ok := w["accounts"]; ok {
		t.Fatalf("not-linked should not include accounts payload")
	}
}

func TestResolveResponseRoundTrip(t *testing.T) {
	bk := &accountsIface.Plan{
		ID: "b-1", AccountID: "a-1", Slug: "prod",
		Mode: accountsIface.PlanModeQuota, Status: accountsIface.PlanStatusActive,
	}
	in := &accountsIface.ResolveResponse{Valid: true, Plan: bk}
	wire := resolveResponseToWire(in)
	out, err := ResolveResponseFromWire(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Valid || out.Plan == nil || out.Plan.Slug != "prod" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestResolveResponseToWire_RejectionShapes(t *testing.T) {
	// Nil response.
	w := resolveResponseToWire(nil)
	if v, _ := w["valid"].(bool); v {
		t.Fatalf("nil should be invalid")
	}
	// Invalid with reason but no plan.
	w = resolveResponseToWire(&accountsIface.ResolveResponse{Valid: false, Reason: "plan not found"})
	if r, _ := w["reason"].(string); r != "plan not found" {
		t.Fatalf("reason missing: %+v", w)
	}
	if _, ok := w["plan"]; ok {
		t.Fatalf("invalid response should not include plan payload")
	}
}

func TestVerifyResponseFromWire_MalformedAccounts(t *testing.T) {
	// accounts field present but not a string → error.
	if _, err := VerifyResponseFromWire(map[string]interface{}{
		"linked":   true,
		"accounts": 42,
	}); err == nil {
		t.Fatalf("expected error for malformed accounts payload")
	}
	// accounts field is bad JSON.
	if _, err := VerifyResponseFromWire(map[string]interface{}{
		"linked":   true,
		"accounts": "{not-json",
	}); err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestResolveResponseFromWire_MalformedPlan(t *testing.T) {
	if _, err := ResolveResponseFromWire(map[string]interface{}{
		"valid": true,
		"plan":  42,
	}); err == nil {
		t.Fatalf("expected error for malformed plan payload")
	}
	if _, err := ResolveResponseFromWire(map[string]interface{}{
		"valid": true,
		"plan":  "{not-json",
	}); err == nil {
		t.Fatalf("expected error for invalid JSON")
	}
}

func TestTryBool(t *testing.T) {
	m := map[string]interface{}{"a": true, "b": false, "c": "not a bool", "d": nil}
	if !tryBool(m, "a") {
		t.Fatalf("a should be true")
	}
	if tryBool(m, "b") {
		t.Fatalf("b should be false")
	}
	if tryBool(m, "c") {
		t.Fatalf("c should be false (wrong type)")
	}
	if tryBool(m, "missing") {
		t.Fatalf("missing should be false")
	}
}

func TestApiVerifyHandler_HappyAndMissingFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	// Seed the store so verify returns linked.
	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bk, _ := newPlanStore(srv.db, acc.ID).Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})
	user, _ := newUserStore(srv.db, acc.ID).Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "1"})
	_ = newUserStore(srv.db, acc.ID).Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: bk.ID})

	// Linked path.
	resp, err := srv.apiVerifyHandler(ctx, nil, command.Body{
		"provider":    "github",
		"external_id": "1",
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v, _ := resp["linked"].(bool); !v {
		t.Fatalf("expected linked=true, got %+v", resp)
	}

	// Missing provider → error.
	if _, err := srv.apiVerifyHandler(ctx, nil, command.Body{"external_id": "1"}); err == nil {
		t.Fatalf("expected error for missing provider")
	}
	// Missing external_id → error.
	if _, err := srv.apiVerifyHandler(ctx, nil, command.Body{"provider": "github"}); err == nil {
		t.Fatalf("expected error for missing external_id")
	}
}

func TestApiResolveHandler_HappyAndMissingFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	acc, _ := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: "acme", Name: "Acme"})
	bk, _ := newPlanStore(srv.db, acc.ID).Create(ctx, accountsIface.CreatePlanInput{Slug: "prod", Name: "Prod"})
	user, _ := newUserStore(srv.db, acc.ID).Add(ctx, accountsIface.AddUserInput{Provider: "github", ExternalID: "1"})
	_ = newUserStore(srv.db, acc.ID).Grant(ctx, user.ID, accountsIface.GrantPlanInput{PlanID: bk.ID})

	// Valid path.
	resp, err := srv.apiResolveHandler(ctx, nil, command.Body{
		"account_slug": "acme",
		"plan_slug":    "prod",
		"provider":     "github",
		"external_id":  "1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if v, _ := resp["valid"].(bool); !v {
		t.Fatalf("expected valid=true, got %+v", resp)
	}

	// Each missing field → error.
	missing := []string{"account_slug", "plan_slug", "provider", "external_id"}
	for _, drop := range missing {
		body := command.Body{
			"account_slug": "acme",
			"plan_slug":    "prod",
			"provider":     "github",
			"external_id":  "1",
		}
		delete(body, drop)
		if _, err := srv.apiResolveHandler(ctx, nil, body); err == nil {
			t.Fatalf("expected error for missing %q", drop)
		}
	}
}
