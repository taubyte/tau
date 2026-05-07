package accounts

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// memberStore implements accountsIface.Members for one Account.
//
// Persistence is split: the Member's profile (sans passkeys) lives at
// .../members/{member_id}/profile, and each PasskeyCredential lives at
// .../members/{member_id}/passkeys/{credential_id_hex}. Passkey ops only
// touch one key.
//
// Maintained indexes:
//
//	/lookup/email/{sha256(email)}                → JSON [(account_id, member_id), ...]
//	/lookup/external/{provider}/{subject}        → JSON [(account_id, member_id), ...]
type memberStore struct {
	db        kvdb.KVDB
	accountID string
}

func newMemberStore(db kvdb.KVDB, accountID string) *memberStore {
	return &memberStore{db: db, accountID: accountID}
}

var _ accountsIface.Members = (*memberStore)(nil)

// memberIndexEntry is one row in the email or external login index.
type memberIndexEntry struct {
	AccountID string `cbor:"account_id"`
	MemberID  string `cbor:"member_id"`
}

// Invite creates a Member with the given email + role. The Member starts
// with no passkeys; the invitee registers one via the magic-link claim flow.
func (s *memberStore) Invite(ctx context.Context, in accountsIface.InviteMemberInput) (*accountsIface.Member, error) {
	if in.PrimaryEmail == "" {
		return nil, errors.New("accounts: primary_email required")
	}
	if in.Role == "" {
		in.Role = accountsIface.RoleAdmin
	}
	now := time.Now().UTC()
	m := &accountsIface.Member{
		ID:           protocolCommon.GetNewMemberID(s.accountID, in.PrimaryEmail, now.UnixNano()),
		AccountID:    s.accountID,
		Role:         in.Role,
		PrimaryEmail: strings.ToLower(strings.TrimSpace(in.PrimaryEmail)),
		Status:       "pending-claim",
		AddedAt:      now,
	}
	if err := putKV(ctx, s.db, MemberProfilePath(s.accountID, m.ID), m); err != nil {
		return nil, err
	}
	if err := s.addEmailIndex(ctx, m.PrimaryEmail, m.ID); err != nil {
		_ = s.db.Delete(ctx, MemberProfilePath(s.accountID, m.ID))
		return nil, err
	}
	return m, nil
}

// Get loads a Member (including passkeys).
func (s *memberStore) Get(ctx context.Context, memberID string) (*accountsIface.Member, error) {
	var m accountsIface.Member
	if err := getKV(ctx, s.db, MemberProfilePath(s.accountID, memberID), &m); err != nil {
		return nil, err
	}
	pks, err := s.listPasskeys(ctx, memberID)
	if err != nil {
		return nil, err
	}
	m.PasskeyCredentials = pks
	return &m, nil
}

// List returns the IDs of all Members on this Account.
func (s *memberStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, AccountMembersPrefix(s.accountID))
}

// Update applies a partial update. Only Role is currently mutable through
// this surface; passkey + external_subject changes go through their own flows.
func (s *memberStore) Update(ctx context.Context, memberID string, in accountsIface.UpdateMemberInput) (*accountsIface.Member, error) {
	m, err := s.Get(ctx, memberID)
	if err != nil {
		return nil, err
	}
	if in.Role != nil {
		m.Role = *in.Role
	}
	// Persist sans passkeys (they live under their own prefix).
	persisted := *m
	persisted.PasskeyCredentials = nil
	if err := putKV(ctx, s.db, MemberProfilePath(s.accountID, memberID), &persisted); err != nil {
		return nil, err
	}
	return m, nil
}

// Remove deletes the Member's profile, passkeys, and lookup indexes.
func (s *memberStore) Remove(ctx context.Context, memberID string) error {
	m, err := s.Get(ctx, memberID)
	if err != nil {
		return err
	}
	for _, pk := range m.PasskeyCredentials {
		if err := s.db.Delete(ctx, MemberPasskeyPath(s.accountID, memberID, hex.EncodeToString(pk.CredentialID))); err != nil {
			return fmt.Errorf("accounts: delete passkey: %w", err)
		}
	}
	if err := s.db.Delete(ctx, MemberProfilePath(s.accountID, memberID)); err != nil {
		return fmt.Errorf("accounts: delete member: %w", err)
	}
	if err := s.removeEmailIndex(ctx, m.PrimaryEmail, m.ID); err != nil {
		return err
	}
	if m.ExternalSubject != "" {
		// We don't know the provider here without re-reading auth_config;
		// the external index is left slightly stale.
	}
	return nil
}

// listPasskeys reads all PasskeyCredential blobs under a Member.
func (s *memberStore) listPasskeys(ctx context.Context, memberID string) ([]accountsIface.PasskeyCredential, error) {
	keys, err := s.db.List(ctx, MemberPasskeysPrefix(s.accountID, memberID))
	if err != nil {
		return nil, fmt.Errorf("accounts: list passkeys: %w", err)
	}
	out := make([]accountsIface.PasskeyCredential, 0, len(keys))
	for _, k := range keys {
		var pk accountsIface.PasskeyCredential
		if err := getKV(ctx, s.db, k, &pk); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, pk)
	}
	return out, nil
}

// AddPasskey persists a new WebAuthn credential against this Member.
func (s *memberStore) AddPasskey(ctx context.Context, memberID string, pk accountsIface.PasskeyCredential) error {
	if len(pk.CredentialID) == 0 {
		return errors.New("accounts: passkey credential_id required")
	}
	credKey := hex.EncodeToString(pk.CredentialID)
	return putKV(ctx, s.db, MemberPasskeyPath(s.accountID, memberID, credKey), pk)
}

// RemovePasskey deletes one Member's passkey by credential id.
func (s *memberStore) RemovePasskey(ctx context.Context, memberID string, credentialID []byte) error {
	credKey := hex.EncodeToString(credentialID)
	return s.db.Delete(ctx, MemberPasskeyPath(s.accountID, memberID, credKey))
}

// --- email index helpers -------------------------------------------

func (s *memberStore) addEmailIndex(ctx context.Context, email, memberID string) error {
	idx, err := s.readMemberIndex(ctx, LookupEmailPath(email))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	idx = append(idx, memberIndexEntry{AccountID: s.accountID, MemberID: memberID})
	return s.writeMemberIndex(ctx, LookupEmailPath(email), idx)
}

func (s *memberStore) removeEmailIndex(ctx context.Context, email, memberID string) error {
	idx, err := s.readMemberIndex(ctx, LookupEmailPath(email))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	out := idx[:0]
	for _, e := range idx {
		if e.AccountID == s.accountID && e.MemberID == memberID {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return s.db.Delete(ctx, LookupEmailPath(email))
	}
	return s.writeMemberIndex(ctx, LookupEmailPath(email), out)
}

// AddExternalIndex registers a (provider, subject) → (account, member) entry
// in the external login index.
func (s *memberStore) AddExternalIndex(ctx context.Context, provider, subject, memberID string) error {
	idx, err := s.readMemberIndex(ctx, LookupExternalPath(provider, subject))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	idx = append(idx, memberIndexEntry{AccountID: s.accountID, MemberID: memberID})
	return s.writeMemberIndex(ctx, LookupExternalPath(provider, subject), idx)
}

// RemoveExternalIndex removes an external-login mapping.
func (s *memberStore) RemoveExternalIndex(ctx context.Context, provider, subject, memberID string) error {
	idx, err := s.readMemberIndex(ctx, LookupExternalPath(provider, subject))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	out := idx[:0]
	for _, e := range idx {
		if e.AccountID == s.accountID && e.MemberID == memberID {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return s.db.Delete(ctx, LookupExternalPath(provider, subject))
	}
	return s.writeMemberIndex(ctx, LookupExternalPath(provider, subject), out)
}

func (s *memberStore) readMemberIndex(ctx context.Context, key string) ([]memberIndexEntry, error) {
	raw, err := s.db.Get(ctx, key)
	if err != nil {
		if isMissing(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("accounts: read index %s: %w", key, err)
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	var idx []memberIndexEntry
	if err := cbor.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("accounts: unmarshal index %s: %w", key, err)
	}
	return idx, nil
}

func (s *memberStore) writeMemberIndex(ctx context.Context, key string, idx []memberIndexEntry) error {
	raw, err := cbor.Marshal(idx)
	if err != nil {
		return fmt.Errorf("accounts: marshal index: %w", err)
	}
	return s.db.Put(ctx, key, raw)
}
