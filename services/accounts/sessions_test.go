package accounts

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSessionStore_IssueAndVerify(t *testing.T) {
	srv := newTestService(t)
	store := newSessionStore(srv.db, time.Hour)
	ctx := context.Background()

	sess, bearer, err := store.Issue(ctx, "acct-1", "mem-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if sess.AccountID != "acct-1" || sess.MemberID != "mem-1" {
		t.Fatalf("session metadata wrong: %+v", sess)
	}
	if bearer == "" || !strings.HasPrefix(bearer, sessionBearerPrefix) {
		t.Fatalf("bearer wrong: %q", bearer)
	}

	a, m, err := store.Verify(ctx, bearer)
	if err != nil || a != "acct-1" || m != "mem-1" {
		t.Fatalf("Verify: %v %q %q", err, a, m)
	}
}

func TestSessionStore_VerifyRejects(t *testing.T) {
	srv := newTestService(t)
	store := newSessionStore(srv.db, time.Hour)
	ctx := context.Background()

	_, bearer, err := store.Issue(ctx, "acct-1", "mem-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// flipLastByte returns the bearer with its final character toggled to a
	// guaranteed-different valid base64url char. Avoids the no-op when the
	// random signature happens to end with the same char we're swapping in.
	last := bearer[len(bearer)-1]
	swap := byte('a')
	if last == 'a' {
		swap = 'b'
	}
	cases := []struct {
		name   string
		bearer string
	}{
		{"empty", ""},
		{"wrong-prefix", "tau." + bearer},
		{"missing-sig", strings.Split(bearer, ".")[0] + "." + strings.Split(bearer, ".")[1]},
		{"truncated", bearer[:len(bearer)-2]},
		{"swap-byte", bearer[:len(bearer)-1] + string(swap)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := store.Verify(ctx, tc.bearer); err == nil {
				t.Fatalf("Verify(%q) should error", tc.bearer)
			}
		})
	}
}

func TestSessionStore_Expired(t *testing.T) {
	srv := newTestService(t)
	store := newSessionStore(srv.db, 1*time.Millisecond)
	ctx := context.Background()
	_, bearer, _ := store.Issue(ctx, "acct-1", "mem-1")

	time.Sleep(20 * time.Millisecond)
	if _, _, err := store.Verify(ctx, bearer); err == nil {
		t.Fatalf("expected expired-session error")
	}
}

func TestSessionStore_Revoke(t *testing.T) {
	srv := newTestService(t)
	store := newSessionStore(srv.db, time.Hour)
	ctx := context.Background()

	_, bearer, _ := store.Issue(ctx, "acct-1", "mem-1")

	if _, _, err := store.Verify(ctx, bearer); err != nil {
		t.Fatalf("pre-revoke Verify: %v", err)
	}
	if err := store.Revoke(ctx, bearer); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, _, err := store.Verify(ctx, bearer); err == nil {
		t.Fatalf("Verify after Revoke should fail")
	}
	// Idempotent: revoking again is OK.
	if err := store.Revoke(ctx, bearer); err != nil {
		t.Fatalf("Revoke idempotent: %v", err)
	}
}

func TestSessionStore_Issue_RequiresIDs(t *testing.T) {
	srv := newTestService(t)
	store := newSessionStore(srv.db, time.Hour)
	ctx := context.Background()
	if _, _, err := store.Issue(ctx, "", "mem-1"); err == nil {
		t.Fatalf("expected error for empty account_id")
	}
	if _, _, err := store.Issue(ctx, "acct-1", ""); err == nil {
		t.Fatalf("expected error for empty member_id")
	}
}

func TestSessionStore_DefaultTTL(t *testing.T) {
	srv := newTestService(t)
	// ttl = 0 → default 24h.
	store := newSessionStore(srv.db, 0)
	ctx := context.Background()
	sess, _, err := store.Issue(ctx, "acct-1", "mem-1")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	delta := time.Until(sess.ExpiresAt)
	if delta < 23*time.Hour || delta > 25*time.Hour {
		t.Fatalf("default TTL wrong: %v", delta)
	}
}
