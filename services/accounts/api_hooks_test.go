package accounts

import (
	"context"
	"testing"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	"github.com/taubyte/tau/p2p/streams/command"
)

// Tests for the wire codec (verify/resolve response encoding + decoding) and
// the stream-handler dispatch layer.

// seedAccountUser stands up an Account + a linked git User. community access is
// linkage-only — being linked to an active Account IS the grant, so no
// ee fixture is needed here (the ee pref/plan/grant fixtures live with the ee
// package's own tests). Returns the Account ID.
func seedAccountUser(t *testing.T, srv *AccountsService, accountSlug, provider, externalID string) (accountID string) {
	t.Helper()
	ctx := context.Background()
	acc, err := newAccountStore(srv.db).Create(ctx, accountsIface.CreateAccountInput{Slug: accountSlug, Name: accountSlug})
	if err != nil {
		t.Fatalf("seed: create account: %v", err)
	}
	if _, err := newUserStore(srv.db, acc.ID).Add(ctx, accountsIface.AddUserInput{Provider: provider, ExternalID: externalID}); err != nil {
		t.Fatalf("seed: add user: %v", err)
	}
	return acc.ID
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
	// Invalid with reason.
	w = resolveResponseToWire(&accountsIface.ResolveResponse{Valid: false, Reason: "account not active"})
	if r, _ := w["reason"].(string); r != "account not active" {
		t.Fatalf("reason missing: %+v", w)
	}
}

func TestApiVerifyHandler_HappyAndMissingFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	seedAccountUser(t, srv, "acme", "github", "1")

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

// TestApiResolveHandler_HappyAndMissingFields covers the community linkage-only
// resolve verb: body is {account_slug, provider, external_id} (no pref_name),
// response is {valid, reason}. The ee resolve verb is covered in the ee tree.
func TestApiResolveHandler_HappyAndMissingFields(t *testing.T) {
	srv := newTestService(t)
	ctx := context.Background()

	seedAccountUser(t, srv, "acme", "github", "1")

	// Valid path.
	resp, err := srv.apiResolveHandler(ctx, nil, command.Body{
		"account_slug": "acme",
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
	missing := []string{"account_slug", "provider", "external_id"}
	for _, drop := range missing {
		body := command.Body{
			"account_slug": "acme",
			"provider":     "github",
			"external_id":  "1",
		}
		delete(body, drop)
		if _, err := srv.apiResolveHandler(ctx, nil, body); err == nil {
			t.Fatalf("expected error for missing %q", drop)
		}
	}
}
