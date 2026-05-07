package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// Member-session bearer format:
//
//   tau-session.<base64url(payload)>.<base64url(hmac)>
//
// payload: {a: account_id, m: member_id, e: exp_unix_ms, j: jti}.
// HMAC-SHA256 over the payload, key = per-Account signing key (signing.go).
//
// Revocation list: a logout writes the jti at /accounts/{aid}/revoked_sessions/{jti}.
// Verify checks both signature/expiry AND that the jti is not in the list.

const sessionBearerPrefix = "tau-session."

// SessionPath returns the KV path for the session revocation marker.
func sessionRevokedPath(accountID, jti string) string {
	return prefixAccounts + accountID + "/revoked_sessions/" + jti
}

// sessionPayload is the decoded body of a Member-session bearer.
type sessionPayload struct {
	A string `json:"a"` // account_id
	M string `json:"m"` // member_id
	E int64  `json:"e"` // exp unix ms
	J string `json:"j"` // jti (random session id)
}

// sessionStore handles Member-session issuance and verification against the
// Accounts KV. Stateless except for the revocation marker.
type sessionStore struct {
	db  kvdb.KVDB
	ttl time.Duration
}

// newSessionStore returns a session store with the configured TTL (default 24h).
func newSessionStore(db kvdb.KVDB, ttl time.Duration) *sessionStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &sessionStore{db: db, ttl: ttl}
}

// Issue creates a Member session and returns the bearer string. The session
// metadata returned reflects the issued session including expiration.
func (s *sessionStore) Issue(ctx context.Context, accountID, memberID string) (*accountsIface.Session, string, error) {
	if accountID == "" || memberID == "" {
		return nil, "", errors.New("accounts: account_id and member_id required for Issue")
	}
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return nil, "", fmt.Errorf("accounts: random jti: %w", err)
	}
	jti := hex.EncodeToString(jtiBytes)

	now := time.Now().UTC()
	exp := now.Add(s.ttl)

	key, err := loadOrCreateAccountSigningKey(ctx, s.db, accountID)
	if err != nil {
		return nil, "", err
	}

	payload, _ := json.Marshal(sessionPayload{A: accountID, M: memberID, E: exp.UnixMilli(), J: jti})
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	sig := mac.Sum(nil)
	bearer := sessionBearerPrefix +
		base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(sig)

	sess := &accountsIface.Session{
		ID:        protocolCommon.GetNewSessionID(accountID, memberID, now.UnixNano()),
		MemberID:  memberID,
		AccountID: accountID,
		IssuedAt:  now,
		ExpiresAt: exp,
		Token:     bearer,
	}
	return sess, bearer, nil
}

// Verify parses a bearer, checks signature, expiry and revocation. Returns
// the (account_id, member_id) if valid.
func (s *sessionStore) Verify(ctx context.Context, bearer string) (accountID, memberID string, err error) {
	if !strings.HasPrefix(bearer, sessionBearerPrefix) {
		return "", "", errors.New("accounts: bad session prefix")
	}
	parts := strings.Split(bearer[len(sessionBearerPrefix):], ".")
	if len(parts) != 2 {
		return "", "", errors.New("accounts: bad session format")
	}
	// Strict() rejects non-canonical base64; without it, a bearer with only
	// the discarded tail bits flipped decodes identically and skips HMAC.
	payloadRaw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("accounts: decode session payload: %w", err)
	}
	sigRaw, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("accounts: decode session sig: %w", err)
	}
	var p sessionPayload
	if err := json.Unmarshal(payloadRaw, &p); err != nil {
		return "", "", fmt.Errorf("accounts: session payload json: %w", err)
	}
	if p.A == "" || p.M == "" || p.J == "" {
		return "", "", errors.New("accounts: session payload missing fields")
	}
	if time.Now().UnixMilli() > p.E {
		return "", "", errors.New("accounts: session expired")
	}
	key, err := loadOrCreateAccountSigningKey(ctx, s.db, p.A)
	if err != nil {
		return "", "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payloadRaw)
	expected := mac.Sum(nil)
	if !hmac.Equal(expected, sigRaw) {
		return "", "", errors.New("accounts: session signature mismatch")
	}
	// Revocation check.
	if _, gerr := s.db.Get(ctx, sessionRevokedPath(p.A, p.J)); gerr == nil {
		return "", "", errors.New("accounts: session revoked")
	} else if !isMissing(gerr) {
		return "", "", fmt.Errorf("accounts: session revoke check: %w", gerr)
	}
	return p.A, p.M, nil
}

// Revoke marks a session bearer as revoked. Idempotent — re-revoking is a no-op.
// Accepts the bearer string; extracts (account_id, jti) from the payload.
func (s *sessionStore) Revoke(ctx context.Context, bearer string) error {
	if !strings.HasPrefix(bearer, sessionBearerPrefix) {
		return errors.New("accounts: bad session prefix")
	}
	parts := strings.Split(bearer[len(sessionBearerPrefix):], ".")
	if len(parts) != 2 {
		return errors.New("accounts: bad session format")
	}
	payloadRaw, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("accounts: decode session payload: %w", err)
	}
	var p sessionPayload
	if err := json.Unmarshal(payloadRaw, &p); err != nil {
		return fmt.Errorf("accounts: session payload json: %w", err)
	}
	if p.A == "" || p.J == "" {
		return errors.New("accounts: session payload missing fields")
	}
	// Mark revoked. We persist a small record (account_id, jti, revoked_at)
	// so admins can audit; verify only checks key existence.
	val, _ := cbor.Marshal(struct {
		AccountID string `cbor:"account_id"`
		JTI       string `cbor:"jti"`
		RevokedAt int64  `cbor:"revoked_at_ms"`
	}{p.A, p.J, time.Now().UnixMilli()})
	return s.db.Put(ctx, sessionRevokedPath(p.A, p.J), val)
}
