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

// KV layout:
//
//   /auth/magic_links/{sha256(code)}                            → magicLinkRecord
//   /auth/rate_limits/email/{sha256(email)}/{hour}/{attempt_id} → ms-timestamp
//   /auth/rate_limits/ip/{ip}/{hour}/{attempt_id}               → ms-timestamp
//   /auth/rate_limits/verify_ip/{ip}/{minute}/{attempt_id}      → ms-timestamp
//
// Rate buckets are key-per-attempt (not a counter blob) so concurrent bumps
// from different nodes can't lose each other's increments — "count" = List.
// Window expiry is implicit via the {hour}/{minute} path segment; stale
// windows simply stop being read (a sweeper or raft phase can prune).

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

// magicLinkRecord — the key is sha256(code), so the raw code is never
// recoverable from the KV.
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

// SendMagicLink: clientIP may be empty for callers that don't carry one (P2P
// CLI flow) — only the per-email rate limit applies in that case.
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

	if err := s.bumpAndCheckRate(ctx, rateLimitEmailPath+emailHash+"/"+hourPlan, magicLinkSendsPerEmailHr); err != nil {
		return err
	}
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
		// Roll back persisted code so the user can retry without burning
		// their per-email budget until expiry.
		_ = s.db.Delete(ctx, magicLinkPathPrefix+sha256Hex(code))
		return err
	}
	return nil
}

// VerifyMagicLink rate-limits per-IP per minute on failed attempts only
// (successes don't bump the counter). Without this, the 10^6 search space
// of a 6-digit code is brute-forceable.
//
// KNOWN RACE (no sub-key fix; raft phase 2 closes it):
// Two service nodes that both receive the same valid code before CRDT
// convergence can each pass the `UsedAt == 0` check and each mark the
// record used. Both will issue a session. Sub-keying the "used" marker
// doesn't help — both nodes idempotently write distinct claim keys and
// both still claim success. The only correct fix is single-leader
// serialization (raft) or a CAS primitive on the KV. Until that lands,
// the code's TTL (short) plus the verify-failure rate limit (above) bound
// the blast radius. Don't try to "fix" this with another layer of
// sub-keyed markers — the issue is exactly-once consumption across
// partitions, not key-shape.
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

// checkVerifyRateLimit uses key-per-attempt under rateLimitVerifyIPPath/
// {ip}/{minute}/{attempt_id}; count by List, not by counter Get+Put. CRDT-
// safe (distinct keys can't collide) at the cost of an O(N) list per check,
// where N is bounded by the per-window limit.
func (s *magicLinkStore) checkVerifyRateLimit(ctx context.Context, clientIP string) error {
	keys, err := s.db.List(ctx, s.verifyRatePrefix(clientIP))
	if err != nil {
		return fmt.Errorf("accounts: verify rate-limit list: %w", err)
	}
	if len(keys) >= verifyAttemptsPerIPMinute {
		return fmt.Errorf("accounts: too many verify attempts; try again in a minute")
	}
	return nil
}

// bumpVerifyFailureCounter is best-effort: write errors are swallowed so the
// caller still sees the original user-facing verify error.
func (s *magicLinkStore) bumpVerifyFailureCounter(ctx context.Context, clientIP string) {
	if clientIP == "" {
		return
	}
	_ = s.db.Put(ctx, s.verifyRatePrefix(clientIP)+newRateAttemptID(), nowMSBytes())
}

// verifyRatePrefix — trailing slash bounds List to this minute's bucket.
func (s *magicLinkStore) verifyRatePrefix(clientIP string) string {
	minute := fmt.Sprintf("%d", time.Now().Unix()/60)
	return rateLimitVerifyIPPath + clientIP + "/" + minute + "/"
}

// bumpAndCheckRate: key-per-attempt under the window prefix, count by List.
// Sub-keyed (not counter Get+Put) so concurrent bumps from different nodes
// can't lose each other's increments.
func (s *magicLinkStore) bumpAndCheckRate(ctx context.Context, prefix string, limit int) error {
	if prefix == "" || prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return fmt.Errorf("accounts: rate-limit list: %w", err)
	}
	if len(keys) >= limit {
		return fmt.Errorf("accounts: rate limit exceeded (%d/hour)", limit)
	}
	if err := s.db.Put(ctx, prefix+newRateAttemptID(), nowMSBytes()); err != nil {
		return fmt.Errorf("accounts: rate-limit write: %w", err)
	}
	return nil
}

// newRateAttemptID — 16-byte crypto-random hex makes collisions effectively
// impossible across concurrent writers on different nodes within a minute.
func newRateAttemptID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// nowMSBytes — the rate-attempt value is human-readable for KV inspection;
// counting is via List, not summation, so the format is informational.
func nowMSBytes() []byte {
	return []byte(fmt.Sprintf("%d", time.Now().UnixMilli()))
}

func (s *magicLinkStore) buildMagicLinkURL(code string) string {
	base := strings.TrimRight(s.accountsURL, "/")
	if base == "" {
		// Dev/dream fallback when AccountsURL isn't configured — the test
		// harness reads the code straight out of the captured email body.
		base = "https://accounts.localhost"
	}
	return base + "/auth/magic?code=" + code
}

func generateMagicLinkCode() (string, error) {
	// crypto/rand.Int over [0, 10^digits) — using byte-mask + modulo would
	// introduce modulo bias on a 10-base space.
	max := big.NewInt(pow10(magicLinkCodeDigits))
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("accounts: random magic-link: %w", err)
	}
	return fmt.Sprintf("%0*d", magicLinkCodeDigits, n.Int64()), nil
}

// pow10 inlined to avoid pulling math.Pow / float64 ↔ int64.
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
