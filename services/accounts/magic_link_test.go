package accounts

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taubyte/tau/services/accounts/email"
)

// extractCodeFromURL pulls `code=...` out of a magic-link URL embedded in
// the email body. Tests use this to simulate clicking the link.
func extractCodeFromBody(t *testing.T, body string) string {
	t.Helper()
	idx := strings.Index(body, "code=")
	if idx == -1 {
		t.Fatalf("body does not contain code: %s", body)
	}
	rest := body[idx+5:]
	end := strings.IndexAny(rest, "\n \r")
	if end == -1 {
		end = len(rest)
	}
	return rest[:end]
}

func TestMagicLink_SendAndVerify(t *testing.T) {
	srv := newTestService(t)
	var buf bytes.Buffer
	sender := email.NewStdoutSender(&buf)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "alice@example.com", "1.2.3.4"); err != nil {
		t.Fatalf("SendMagicLink: %v", err)
	}
	sent := sender.Sent()
	if len(sent) != 1 {
		t.Fatalf("want 1 message, got %d", len(sent))
	}
	if sent[0].To != "alice@example.com" {
		t.Fatalf("To wrong: %q", sent[0].To)
	}

	code := extractCodeFromBody(t, sent[0].Body)

	a, m, err := store.VerifyMagicLink(ctx, code, "")
	if err != nil || a != "acct-1" || m != "mem-1" {
		t.Fatalf("Verify: %v %q %q", err, a, m)
	}

	// Single-use: re-verifying fails.
	if _, _, err := store.VerifyMagicLink(ctx, code, ""); err == nil {
		t.Fatalf("magic-link should be single-use")
	}
}

func TestMagicLink_Expired(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "alice@example.com", ""); err != nil {
		t.Fatalf("SendMagicLink: %v", err)
	}

	// Manipulate the persisted record to simulate expiry.
	sent := sender.Sent()
	code := extractCodeFromBody(t, sent[0].Body)
	key := magicLinkPathPrefix + sha256Hex(code)
	raw, _ := srv.db.Get(ctx, key)
	// rewrite with exp in the past
	expired := strings.Replace(string(raw), "\"exp_ms\":", "\"exp_ms\":1,", 1)
	if expired == string(raw) {
		// Fallback: just push a fresh expired record.
		expired = `{"account_id":"acct-1","member_id":"mem-1","exp_ms":1}`
	}
	_ = srv.db.Put(ctx, key, []byte(`{"account_id":"acct-1","member_id":"mem-1","exp_ms":1}`))

	if _, _, err := store.VerifyMagicLink(ctx, code, ""); err == nil {
		t.Fatalf("expected expired-magic-link error")
	}
}

func TestMagicLink_VerifyMissing(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	if _, _, err := store.VerifyMagicLink(ctx, "nope", ""); err == nil {
		t.Fatalf("expected not-found error")
	}
	if _, _, err := store.VerifyMagicLink(ctx, "", ""); err == nil {
		t.Fatalf("expected empty-code error")
	}
}

func TestMagicLink_RateLimitPerEmail(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	// Hardcoded `magicLinkSendsPerEmailHr = 5` — exhaust then expect
	// rejection on the 6th call.
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	for i := 0; i < magicLinkSendsPerEmailHr; i++ {
		if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "alice@example.com", ""); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "alice@example.com", ""); err == nil {
		t.Fatalf("expected rate-limit error after %d sends", magicLinkSendsPerEmailHr)
	}
}

func TestMagicLink_RateLimitPerIP(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	// `magicLinkSendsPerIPHr = 20` sends from one IP across distinct
	// emails. Each (email, IP) pair counts against the IP cap; using
	// distinct emails isolates this test from the per-email cap (which
	// would otherwise trigger first at 5 sends to the same address).
	for i := 0; i < magicLinkSendsPerIPHr; i++ {
		addr := fmt.Sprintf("user-%d@x", i)
		if err := store.SendMagicLink(ctx, "acct-1", "mem-1", addr, "9.9.9.9"); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "overflow@x", "9.9.9.9"); err == nil {
		t.Fatalf("expected per-IP rate-limit error after %d sends", magicLinkSendsPerIPHr)
	}
}

func TestMagicLink_RequiredFields(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	if err := store.SendMagicLink(ctx, "", "mem-1", "alice@x", ""); err == nil {
		t.Fatalf("expected error for empty account_id")
	}
	if err := store.SendMagicLink(ctx, "acct-1", "", "alice@x", ""); err == nil {
		t.Fatalf("expected error for empty member_id")
	}
	if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "", ""); err == nil {
		t.Fatalf("expected error for empty email")
	}
}

func TestMagicLink_NoSender(t *testing.T) {
	srv := newTestService(t)
	store := newMagicLinkStore(srv.db, nil, "https://accounts.test.tau")
	ctx := context.Background()
	if err := store.SendMagicLink(ctx, "acct-1", "mem-1", "alice@example.com", ""); err == nil {
		t.Fatalf("expected error when sender is nil")
	}
}

// (Defaults-test removed: limits are now compile-time constants
// `magicLinkSendsPerEmailHr` / `magicLinkSendsPerIPHr` rather than
// per-instance settable fields. The two rate-limit tests above
// exercise the constants in their actual enforcement path.)

func TestMagicLink_BuildURLNoConfig(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "")
	url := store.buildMagicLinkURL("abc")
	if !strings.Contains(url, "?code=abc") {
		t.Fatalf("URL missing code: %s", url)
	}
}

func TestMagicLink_TTL(t *testing.T) {
	if magicLinkTTL != 5*time.Minute {
		t.Fatalf("default TTL changed unexpectedly: %v", magicLinkTTL)
	}
}

func TestMagicLink_CodeFormat(t *testing.T) {
	// Codes are exactly N decimal digits — same shape as 2FA OTPs (banking,
	// Google sign-in, etc.). Easy to copy-paste from email into a CLI prompt.
	for range 20 {
		code, err := generateMagicLinkCode()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(code) != magicLinkCodeDigits {
			t.Fatalf("code length = %d, want %d (%q)", len(code), magicLinkCodeDigits, code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("code has non-digit %q in %q", string(r), code)
			}
		}
	}
}

func TestMagicLink_VerifyRateLimit(t *testing.T) {
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	// Burn the per-IP verify-failure budget on bogus codes; next call from
	// the same IP gets the rate-limit error.
	const ip = "203.0.113.42"
	for i := 0; i < verifyAttemptsPerIPMinute; i++ {
		if _, _, err := store.VerifyMagicLink(ctx, "wrong-"+string(rune('a'+i)), ip); err == nil {
			t.Fatalf("attempt %d: bogus code should fail", i)
		}
	}
	_, _, err := store.VerifyMagicLink(ctx, "wrong-final", ip)
	if err == nil {
		t.Fatalf("expected rate-limit error after %d failures", verifyAttemptsPerIPMinute)
	}
	if !strings.Contains(err.Error(), "too many verify attempts") {
		t.Fatalf("expected rate-limit message, got: %v", err)
	}

	// Different IP is unaffected.
	if _, _, err := store.VerifyMagicLink(ctx, "wrong-other", "198.51.100.7"); err == nil {
		t.Fatalf("a different IP should not be limited")
	} else if strings.Contains(err.Error(), "too many verify attempts") {
		t.Fatalf("a different IP should not hit the rate limit: %v", err)
	}
}

func TestMagicLink_VerifyRateLimit_NoIP(t *testing.T) {
	// Empty clientIP (e.g. P2P or in-process callers) bypasses the gate —
	// the wire transport is responsible for client isolation in that case.
	srv := newTestService(t)
	sender := email.NewStdoutSender(nil)
	store := newMagicLinkStore(srv.db, sender, "https://accounts.test.tau")
	ctx := context.Background()

	for i := 0; i < verifyAttemptsPerIPMinute*3; i++ {
		_, _, err := store.VerifyMagicLink(ctx, "wrong", "")
		if err == nil {
			t.Fatalf("bogus code should always fail")
		}
		if strings.Contains(err.Error(), "too many verify attempts") {
			t.Fatalf("empty clientIP should bypass rate-limit, got: %v", err)
		}
	}
}
