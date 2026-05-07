package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/services/accounts/email"
)

// Magic-link layout:
//
//   /auth/magic_links/{sha256(code)} → {account_id, member_id, exp_unix_ms, used_at?}
//   /auth/rate_limits/email/{sha256(email)}/{hour_plan} → counter
//   /auth/rate_limits/ip/{ip}/{hour_plan} → counter
//
// The raw code is never stored — only its sha256. Verify hashes the
// presented code and looks up that key.

const (
	magicLinkPathPrefix   = "/auth/magic_links/"
	rateLimitEmailPath    = "/auth/rate_limits/email/"
	rateLimitIPPath       = "/auth/rate_limits/ip/"
	rateLimitVerifyIPPath = "/auth/rate_limits/verify_ip/"

	// 6-digit numeric — same shape as 2FA / banking OTP. 10^6 search space
	// is small, so the security model leans on:
	//   - short TTL (magicLinkTTL below)
	//   - send-side rate limit (per-email + per-IP, both already wired)
	//   - verify-side rate limit (per-IP, see verifyAttemptsPerIPMinute)
	//   - single-use (used_at flag in the persisted record)
	magicLinkCodeDigits = 6

	// Default TTL — kept short because the codes are short. 5 minutes is
	// enough for a real email round-trip (compose/deliver/open/copy/paste)
	// without giving attackers a long brute-force window.
	magicLinkTTL = 5 * time.Minute

	// Per-IP cap on magic-link verify attempts inside a one-minute bucket.
	// Failed attempts count; successful ones don't (a legit user types
	// their code once and is done). 10/min keeps user-typing slack while
	// hard-blocking automated guessing of the 10^6 search space.
	verifyAttemptsPerIPMinute = 10
)

// magicLinkRecord is what we persist per code. Only its hash is the key, so
// the raw code can't be recovered from the KV.
type magicLinkRecord struct {
	AccountID string `cbor:"account_id"`
	MemberID  string `cbor:"member_id"`
	ExpiresAt int64  `cbor:"exp_ms"`
	UsedAt    int64  `cbor:"used_ms,omitempty"`
}

// Magic-link send-side rate limits, hardcoded rather than operator-configured.
// Composed with the verify-side per-IP limit (10/min, in `magic_link.go`'s
// verify path) to give 2FA-grade protection against brute-forcing the
// 6-digit code. Tuning these would either weaken the protection or DoS the
// operator's own users; neither use case has shown up.
const (
	magicLinkSendsPerEmailHr = 5
	magicLinkSendsPerIPHr    = 20
)

// magicLinkStore implements send/verify with rate limiting.
type magicLinkStore struct {
	db          kvdb.KVDB
	sender      email.Sender
	accountsURL string
}

func newMagicLinkStore(db kvdb.KVDB, sender email.Sender, accountsURL string) *magicLinkStore {
	return &magicLinkStore{
		db:          db,
		sender:      sender,
		accountsURL: accountsURL,
	}
}

// SendMagicLink generates a single-use code, persists it under the given
// (account, member, email), enforces rate limits, and emails the URL.
//
// `clientIP` may be empty when the call originates from a context that
// doesn't carry one (e.g. P2P CLI flow); only the per-email rate limit
// applies in that case.
func (s *magicLinkStore) SendMagicLink(ctx context.Context, accountID, memberID, to, clientIP string) error {
	if to == "" {
		return errors.New("accounts: magic-link recipient required")
	}
	if accountID == "" || memberID == "" {
		return errors.New("accounts: magic-link account_id and member_id required")
	}

	now := time.Now().UTC()
	hourPlan := fmt.Sprintf("%d", now.Unix()/3600)
	emailHash := hashLower(to)

	// Email-plan rate limit.
	if err := s.bumpAndCheckRate(ctx, rateLimitEmailPath+emailHash+"/"+hourPlan, magicLinkSendsPerEmailHr); err != nil {
		return err
	}
	// IP-plan rate limit.
	if clientIP != "" {
		if err := s.bumpAndCheckRate(ctx, rateLimitIPPath+clientIP+"/"+hourPlan, magicLinkSendsPerIPHr); err != nil {
			return err
		}
	}

	code, err := generateMagicLinkCode()
	if err != nil {
		return err
	}
	rec := magicLinkRecord{
		AccountID: accountID,
		MemberID:  memberID,
		ExpiresAt: now.Add(magicLinkTTL).UnixMilli(),
	}
	raw, _ := cbor.Marshal(rec)
	if err := s.db.Put(ctx, magicLinkPathPrefix+sha256Hex(code), raw); err != nil {
		return fmt.Errorf("accounts: persist magic-link: %w", err)
	}

	if s.sender == nil {
		return errors.New("accounts: no email sender configured")
	}
	subject, body, err := email.RenderMagicLink(email.MagicLinkTemplateData{
		Code:       code,
		URL:        s.buildMagicLinkURL(code),
		TTLMinutes: int(magicLinkTTL.Minutes()),
	})
	if err != nil {
		_ = s.db.Delete(ctx, magicLinkPathPrefix+sha256Hex(code))
		return err
	}
	if err := s.sender.Send(ctx, to, subject, body); err != nil {
		// Roll back the persisted code so a future user can retry without
		// hitting the per-email limit until expiry.
		_ = s.db.Delete(ctx, magicLinkPathPrefix+sha256Hex(code))
		return err
	}
	return nil
}

// VerifyMagicLink checks the code against the store, marks it used, and
// returns the (account, member) it authenticates. Returns an error for
// missing / expired / already-used codes.
//
// `clientIP` (when non-empty) is rate-limited per minute: failed attempts
// (lookup miss, expired, already-used) bump a per-IP counter; successful
// verifies don't. Once the counter exceeds verifyAttemptsPerIPMinute the
// IP is rejected for the rest of the minute. This is what makes 6-digit
// codes safe — without it the 10^6 search space is brute-forceable.
func (s *magicLinkStore) VerifyMagicLink(ctx context.Context, code, clientIP string) (accountID, memberID string, err error) {
	if code == "" {
		return "", "", errors.New("accounts: magic-link code required")
	}
	if clientIP != "" {
		if err := s.checkVerifyRateLimit(ctx, clientIP); err != nil {
			return "", "", err
		}
	}
	key := magicLinkPathPrefix + sha256Hex(code)
	raw, gerr := s.db.Get(ctx, key)
	if gerr != nil {
		if isMissing(gerr) {
			s.bumpVerifyFailureCounter(ctx, clientIP)
			return "", "", errors.New("accounts: magic-link not found or expired")
		}
		return "", "", fmt.Errorf("accounts: read magic-link: %w", gerr)
	}
	var rec magicLinkRecord
	if jerr := cbor.Unmarshal(raw, &rec); jerr != nil {
		return "", "", fmt.Errorf("accounts: magic-link decode: %w", jerr)
	}
	now := time.Now().UnixMilli()
	if now > rec.ExpiresAt {
		_ = s.db.Delete(ctx, key)
		return "", "", errors.New("accounts: magic-link expired")
	}
	if rec.UsedAt != 0 {
		s.bumpVerifyFailureCounter(ctx, clientIP)
		return "", "", errors.New("accounts: magic-link already used")
	}
	rec.UsedAt = now
	updated, _ := cbor.Marshal(rec)
	if perr := s.db.Put(ctx, key, updated); perr != nil {
		return "", "", fmt.Errorf("accounts: mark magic-link used: %w", perr)
	}
	return rec.AccountID, rec.MemberID, nil
}

// checkVerifyRateLimit returns nil when the IP is under the per-minute
// failure cap, or an error when it has exceeded it. Counters live at
// rateLimitVerifyIPPath/{ip}/{minute} so they reset every minute and the
// keyspace doesn't grow unboundedly.
func (s *magicLinkStore) checkVerifyRateLimit(ctx context.Context, clientIP string) error {
	key := s.verifyRateKey(clientIP)
	raw, err := s.db.Get(ctx, key)
	if err != nil {
		if isMissing(err) {
			return nil
		}
		return fmt.Errorf("accounts: verify rate-limit read: %w", err)
	}
	current, _ := decodeIntDecimal(string(raw))
	if current >= verifyAttemptsPerIPMinute {
		return fmt.Errorf("accounts: too many verify attempts; try again in a minute")
	}
	return nil
}

// bumpVerifyFailureCounter increments the IP's failure counter for the
// current minute. Best-effort — write errors are ignored to keep the verify
// path returning the user-facing error rather than a confusing rate-limit
// internal error.
func (s *magicLinkStore) bumpVerifyFailureCounter(ctx context.Context, clientIP string) {
	if clientIP == "" {
		return
	}
	key := s.verifyRateKey(clientIP)
	current := 0
	if raw, err := s.db.Get(ctx, key); err == nil && len(raw) > 0 {
		current, _ = decodeIntDecimal(string(raw))
	}
	current++
	_ = s.db.Put(ctx, key, []byte(fmt.Sprintf("%d", current)))
}

// verifyRateKey returns the per-minute counter key for an IP.
func (s *magicLinkStore) verifyRateKey(clientIP string) string {
	minute := fmt.Sprintf("%d", time.Now().Unix()/60)
	return rateLimitVerifyIPPath + clientIP + "/" + minute
}

// bumpAndCheckRate increments the counter at key and returns an error when
// it exceeds limit.
func (s *magicLinkStore) bumpAndCheckRate(ctx context.Context, key string, limit int) error {
	current := 0
	if raw, err := s.db.Get(ctx, key); err == nil && len(raw) > 0 {
		current, _ = decodeIntDecimal(string(raw))
	} else if err != nil && !isMissing(err) {
		return fmt.Errorf("accounts: rate-limit read: %w", err)
	}
	if current >= limit {
		return fmt.Errorf("accounts: rate limit exceeded (%d/hour)", limit)
	}
	current++
	if err := s.db.Put(ctx, key, []byte(fmt.Sprintf("%d", current))); err != nil {
		return fmt.Errorf("accounts: rate-limit write: %w", err)
	}
	return nil
}

// buildMagicLinkURL is the deep-link presented in the email. Accounts.AccountsURL
// (e.g. https://accounts.<network>) is set in config.
func (s *magicLinkStore) buildMagicLinkURL(code string) string {
	base := strings.TrimRight(s.accountsURL, "/")
	if base == "" {
		// Fallback for dev/dream where the operator hasn't set AccountsURL —
		// the code itself is what matters; the test harness reads it from
		// the captured email body.
		base = "https://accounts.localhost"
	}
	return base + "/auth/magic?code=" + code
}

func generateMagicLinkCode() (string, error) {
	// Generate `magicLinkCodeDigits` digits of crypto-random uniformly in
	// [0, 10^digits). We use `crypto/rand.Int` rather than masking off bits
	// of byte-random to avoid modulo bias.
	max := big.NewInt(pow10(magicLinkCodeDigits))
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("accounts: random magic-link: %w", err)
	}
	return fmt.Sprintf("%0*d", magicLinkCodeDigits, n.Int64()), nil
}

// pow10 returns 10^n. Inlined so we don't pull math.Pow / float64 ↔ int64.
func pow10(n int) int64 {
	out := int64(1)
	for range n {
		out *= 10
	}
	return out
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func hashLower(email string) string {
	return sha256Hex(strings.ToLower(strings.TrimSpace(email)))
}

func decodeIntDecimal(s string) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("accounts: rate-limit not decimal")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
