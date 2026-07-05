package accounts

import (
	"context"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

// Tests for the wire codec (verify/resolve response encoding + decoding) and
// the stream-handler dispatch layer.

// seedAccountUserPRef stands up an Account + Plan + PRef (assigned) + User
// (with a grant on that PRef). Returns the IDs/names so callers can build
// resolve/verify queries against them. Centralised so test files share one
// setup convention.
func seedAccountUserPRef(t *testing.T, srv *AccountsService, accountSlug, prefName, provider, externalID string) (accountID, planID string) {
	t.Helper()
	ctx := context.Background()
	acc, err := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: accountSlug, Name: accountSlug})
	if err != nil {
		t.Fatalf("seed: create account: %v", err)
	}
	plan, err := newPlanStore(srv.db).Create(ctx, accountsIface.CreatePlanInput{Name: "Prod"})
	if err != nil {
		t.Fatalf("seed: create plan: %v", err)
	}
	prefs := newPRefStore(srv.db, acc.ID, newPlanStore(srv.db))
	if _, err := prefs.Create(ctx, accountsIface.CreatePRefInput{
		Name:     prefName,
		MemberID: "system:test",
	}); err != nil {
		t.Fatalf("seed: create pref: %v", err)
	}
	if _, err := prefs.Assign(ctx, accountsIface.AssignPRefInput{
		Name:     prefName,
		PlanID:   plan.ID,
		MemberID: "system:test",
	}); err != nil {
		t.Fatalf("seed: assign pref: %v", err)
	}
	user, err := newUserStore(srv.db, acc.ID).Add(ctx, accountsIface.AddUserInput{Provider: provider, ExternalID: externalID})
	if err != nil {
		t.Fatalf("seed: add user: %v", err)
	}
	if err := newUserStore(srv.db, acc.ID).Grant(ctx, user.ID, accountsIface.GrantPRefInput{PRefName: prefName}); err != nil {
		t.Fatalf("seed: grant: %v", err)
	}
	return acc.ID, plan.ID
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

func TestResolveResponseToWire_RejectionShapes(t *testing.T) {
	// Nil response.
	w := resolveResponseToWire(nil)
	if v, _ := w["valid"].(bool); v {
		t.Fatalf("nil should be invalid")
	}
	// Invalid with reason but no plan.
	w = resolveResponseToWire(&accountsIface.ResolveResponse{Valid: false, Reason: "pref not found"})
	if r, _ := w["reason"].(string); r != "pref not found" {
		t.Fatalf("reason missing: %+v", w)
	}
	if _, ok := w["plan"]; ok {
		t.Fatalf("invalid response should not include plan payload")
	}
}

func TestApiVerifyHandler_HappyAndMissingFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	seedAccountUserPRef(t, srv, "acme", "prod", "github", "1")

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

	seedAccountUserPRef(t, srv, "acme", "prod", "github", "1")

	// Valid path.
	resp, err := srv.apiResolveHandler(ctx, nil, command.Body{
		"account_slug": "acme",
		"pref_name":    "prod",
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
	missing := []string{"account_slug", "pref_name", "provider", "external_id"}
	for _, drop := range missing {
		body := command.Body{
			"account_slug": "acme",
			"pref_name":    "prod",
			"provider":     "github",
			"external_id":  "1",
		}
		delete(body, drop)
		if _, err := srv.apiResolveHandler(ctx, nil, body); err == nil {
			t.Fatalf("expected error for missing %q", drop)
		}
	}
}
