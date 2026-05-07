package accounts

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
)

// WebAuthn relying-party wrapper.
//
// We use github.com/go-webauthn/webauthn as the protocol implementation. This
// file glues that to our Member storage and to a small KV-backed session
// store for the per-flow challenge data passed between Begin* and Finish*.
//
// Key paths:
//
//   /auth/webauthn_sessions/{hex(session_id)} → JSON of webauthn.SessionData
//
// Session entries are TTL'd at lookup time (5-minute window for a flow to
// complete). They're not under /accounts/{id}/... because a login flow
// targets one (account, member) tuple but the SessionData itself is
// challenge state — flat namespace is simpler.

const (
	webauthnSessionPathPrefix = "/auth/webauthn_sessions/"
	webauthnSessionTTL        = 5 * time.Minute
)

// webauthnStore wraps go-webauthn with our member-aware storage.
type webauthnStore struct {
	db          kvdb.KVDB
	rp          *webauthn.WebAuthn
	memberStore func(accountID string) *memberStore // injected to avoid an import cycle
}

// newWebAuthnStore builds a relying-party at the given identity. RPID empty
// disables passkey support (managed-mode without WebAuthn — magic-link only
// — is still supported). In normal operation `accountsIface.InferWebAuthn`
// derives a non-empty RPID from the runtime FQDN, so this only kicks in for
// unit tests that want to exercise the magic-link-only fallback explicitly.
func newWebAuthnStore(db kvdb.KVDB, defaults accountsIface.WebAuthnDefaults, memberStoreFn func(accountID string) *memberStore) (*webauthnStore, error) {
	if defaults.RPID == "" {
		return &webauthnStore{db: db, rp: nil, memberStore: memberStoreFn}, nil
	}
	rp, err := webauthn.New(&webauthn.Config{
		RPID:          defaults.RPID,
		RPDisplayName: defaults.RPName,
		RPOrigins:     defaults.Origins,
	})
	if err != nil {
		return nil, fmt.Errorf("accounts: webauthn relying-party init: %w", err)
	}
	return &webauthnStore{db: db, rp: rp, memberStore: memberStoreFn}, nil
}

// Available reports whether passkey login is configured.
func (s *webauthnStore) Available() bool { return s != nil && s.rp != nil }

// memberWebAuthnUser adapts a Member to go-webauthn's User interface.
type memberWebAuthnUser struct {
	id          []byte
	name        string
	displayName string
	credentials []webauthn.Credential
}

func (u *memberWebAuthnUser) WebAuthnID() []byte                         { return u.id }
func (u *memberWebAuthnUser) WebAuthnName() string                       { return u.name }
func (u *memberWebAuthnUser) WebAuthnDisplayName() string                { return u.displayName }
func (u *memberWebAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }
func (u *memberWebAuthnUser) WebAuthnIcon() string                       { return "" }

// loadUser builds a webauthn.User from a Member record.
func (s *webauthnStore) loadUser(ctx context.Context, accountID, memberID string) (*memberWebAuthnUser, error) {
	m, err := s.memberStore(accountID).Get(ctx, memberID)
	if err != nil {
		return nil, err
	}
	creds := make([]webauthn.Credential, 0, len(m.PasskeyCredentials))
	for _, pk := range m.PasskeyCredentials {
		creds = append(creds, webauthn.Credential{
			ID:              pk.CredentialID,
			PublicKey:       pk.PublicKey,
			AttestationType: pk.AttestationType,
			Authenticator: webauthn.Authenticator{
				SignCount: pk.SignCount,
			},
		})
	}
	return &memberWebAuthnUser{
		id:          []byte(memberID),
		name:        m.PrimaryEmail,
		displayName: m.PrimaryEmail, // reuse email as display name in v1
		credentials: creds,
	}, nil
}

// BeginRegistration starts a passkey-registration flow for an existing
// Member. The returned CredentialCreation must be passed to the WebAuthn
// browser API; the sessionID is what the client returns alongside the
// attestation in FinishRegistration.
func (s *webauthnStore) BeginRegistration(ctx context.Context, accountID, memberID string) (sessionID string, options *protocol.CredentialCreation, err error) {
	if !s.Available() {
		return "", nil, errors.New("accounts: webauthn not configured")
	}
	user, err := s.loadUser(ctx, accountID, memberID)
	if err != nil {
		return "", nil, err
	}
	creation, sd, err := s.rp.BeginRegistration(user)
	if err != nil {
		return "", nil, fmt.Errorf("accounts: webauthn begin register: %w", err)
	}
	sessionID, err = s.persistSessionData(ctx, sd, accountID, memberID, "register")
	if err != nil {
		return "", nil, err
	}
	return sessionID, creation, nil
}

// FinishRegistration verifies the attestation and persists the credential on
// the Member. Returns the new PasskeyCredential id (hex of credential ID).
func (s *webauthnStore) FinishRegistration(ctx context.Context, sessionID string, response *protocol.ParsedCredentialCreationData) (credentialIDHex string, err error) {
	if !s.Available() {
		return "", errors.New("accounts: webauthn not configured")
	}
	sd, accountID, memberID, kind, err := s.consumeSessionData(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if kind != "register" {
		return "", errors.New("accounts: webauthn session is not a registration")
	}
	user, err := s.loadUser(ctx, accountID, memberID)
	if err != nil {
		return "", err
	}
	cred, err := s.rp.CreateCredential(user, *sd, response)
	if err != nil {
		return "", fmt.Errorf("accounts: webauthn finish register: %w", err)
	}
	pk := accountsIface.PasskeyCredential{
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		SignCount:       cred.Authenticator.SignCount,
		RegisteredAt:    time.Now().UTC(),
	}
	if err := s.memberStore(accountID).AddPasskey(ctx, memberID, pk); err != nil {
		return "", err
	}
	return hex.EncodeToString(cred.ID), nil
}

// BeginLogin starts a passkey assertion challenge for the given Member.
func (s *webauthnStore) BeginLogin(ctx context.Context, accountID, memberID string) (sessionID string, options *protocol.CredentialAssertion, err error) {
	if !s.Available() {
		return "", nil, errors.New("accounts: webauthn not configured")
	}
	user, err := s.loadUser(ctx, accountID, memberID)
	if err != nil {
		return "", nil, err
	}
	if len(user.credentials) == 0 {
		return "", nil, errors.New("accounts: member has no registered passkeys")
	}
	assertion, sd, err := s.rp.BeginLogin(user)
	if err != nil {
		return "", nil, fmt.Errorf("accounts: webauthn begin login: %w", err)
	}
	sessionID, err = s.persistSessionData(ctx, sd, accountID, memberID, "login")
	if err != nil {
		return "", nil, err
	}
	return sessionID, assertion, nil
}

// FinishLogin verifies an assertion and returns the (account, member) it
// authenticates. Updates the credential's SignCount on success.
func (s *webauthnStore) FinishLogin(ctx context.Context, sessionID string, response *protocol.ParsedCredentialAssertionData) (accountID, memberID string, err error) {
	if !s.Available() {
		return "", "", errors.New("accounts: webauthn not configured")
	}
	sd, accountID, memberID, kind, err := s.consumeSessionData(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if kind != "login" {
		return "", "", errors.New("accounts: webauthn session is not a login")
	}
	user, err := s.loadUser(ctx, accountID, memberID)
	if err != nil {
		return "", "", err
	}
	cred, err := s.rp.ValidateLogin(user, *sd, response)
	if err != nil {
		return "", "", fmt.Errorf("accounts: webauthn finish login: %w", err)
	}
	// Update SignCount to defend against cloned-credential attacks.
	pk := accountsIface.PasskeyCredential{
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		SignCount:       cred.Authenticator.SignCount,
		RegisteredAt:    time.Now().UTC(),
	}
	if err := s.memberStore(accountID).AddPasskey(ctx, memberID, pk); err != nil {
		return "", "", err
	}
	return accountID, memberID, nil
}

// --- Session-data persistence ------------------------------------

type webauthnSession struct {
	AccountID string                `json:"account_id"`
	MemberID  string                `json:"member_id"`
	Kind      string                `json:"kind"` // "register" | "login"
	ExpiresAt int64                 `json:"exp_ms"`
	Data      *webauthn.SessionData `json:"data"`
}

// persistSessionData stores the protocol-level SessionData under a freshly
// generated id and returns the id. Caller hands the id back in Finish*.
func (s *webauthnStore) persistSessionData(ctx context.Context, sd *webauthn.SessionData, accountID, memberID, kind string) (string, error) {
	idBytes := make([]byte, 16)
	if _, err := readRandom(idBytes); err != nil {
		return "", fmt.Errorf("accounts: webauthn session id: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	rec := webauthnSession{
		AccountID: accountID,
		MemberID:  memberID,
		Kind:      kind,
		ExpiresAt: time.Now().Add(webauthnSessionTTL).UnixMilli(),
		Data:      sd,
	}
	raw, _ := json.Marshal(rec)
	if err := s.db.Put(ctx, webauthnSessionPathPrefix+id, raw); err != nil {
		return "", fmt.Errorf("accounts: webauthn session persist: %w", err)
	}
	return id, nil
}

// consumeSessionData looks up + deletes the session; returns the SessionData
// and the (account, member, kind) that originated it. Errors when missing,
// expired, or malformed.
func (s *webauthnStore) consumeSessionData(ctx context.Context, sessionID string) (*webauthn.SessionData, string, string, string, error) {
	key := webauthnSessionPathPrefix + sessionID
	raw, err := s.db.Get(ctx, key)
	if err != nil {
		if isMissing(err) {
			return nil, "", "", "", errors.New("accounts: webauthn session not found")
		}
		return nil, "", "", "", fmt.Errorf("accounts: webauthn session read: %w", err)
	}
	var rec webauthnSession
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, "", "", "", fmt.Errorf("accounts: webauthn session decode: %w", err)
	}
	_ = s.db.Delete(ctx, key) // single-use
	if time.Now().UnixMilli() > rec.ExpiresAt {
		return nil, "", "", "", errors.New("accounts: webauthn session expired")
	}
	return rec.Data, rec.AccountID, rec.MemberID, rec.Kind, nil
}

// readRandom is wrapped here so tests can override it for determinism. The
// production impl just calls crypto/rand.Reader.Read.
var readRandom = func(b []byte) (int, error) {
	return cryptoRandRead(b)
}
