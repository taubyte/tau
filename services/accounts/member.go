package accounts

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// memberStore implements accountsIface.Members for one Account. The Member's
// profile lives separate from its passkeys (each passkey is its own KV key)
// so passkey ops touch a single key. Lookup-index layout in paths.go.
type memberStore struct {
	db        kvdb.KVDB
	accountID string
}

func newMemberStore(db kvdb.KVDB, accountID string) *memberStore {
	return &memberStore{db: db, accountID: accountID}
}

var _ accountsIface.Members = (*memberStore)(nil)

// memberIndexEntry is reconstructed from lookup-index keys; not a stored
// shape — the (account, member) tuple lives in the key path.
type memberIndexEntry struct {
	AccountID string
	MemberID  string
}

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

func (s *memberStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, AccountMembersPrefix(s.accountID))
}

func (s *memberStore) Update(ctx context.Context, memberID string, in accountsIface.UpdateMemberInput) (*accountsIface.Member, error) {
	m, err := s.Get(ctx, memberID)
	if err != nil {
		return nil, err
	}
	if in.Role != nil {
		m.Role = *in.Role
	}
	persisted := *m
	persisted.PasskeyCredentials = nil
	if err := putKV(ctx, s.db, MemberProfilePath(s.accountID, memberID), &persisted); err != nil {
		return nil, err
	}
	return m, nil
}

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
	// Email-index cleanup is best-effort: the profile is already gone, and
	// failing Remove because a secondary index couldn't be pruned would
	// strand the caller. A future sweep or re-Remove can prune the stale row.
	if err := s.removeEmailIndex(ctx, m.PrimaryEmail, m.ID); err != nil {
		logger.Warnf("accounts: stale email index entry for member %s on account %s: %v",
			m.ID, s.accountID, err)
	}
	// External-index cleanup needs the provider, which would mean re-reading
	// auth_config — left slightly stale on purpose.
	return nil
}

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

func (s *memberStore) AddPasskey(ctx context.Context, memberID string, pk accountsIface.PasskeyCredential) error {
	if len(pk.CredentialID) == 0 {
		return errors.New("accounts: passkey credential_id required")
	}
	credKey := hex.EncodeToString(pk.CredentialID)
	return putKV(ctx, s.db, MemberPasskeyPath(s.accountID, memberID, credKey), pk)
}

func (s *memberStore) RemovePasskey(ctx context.Context, memberID string, credentialID []byte) error {
	credKey := hex.EncodeToString(credentialID)
	return s.db.Delete(ctx, MemberPasskeyPath(s.accountID, memberID, credKey))
}

func (s *memberStore) addEmailIndex(ctx context.Context, email, memberID string) error {
	return s.db.Put(ctx, LookupEmailEntryPath(email, s.accountID, memberID), nowBytes())
}

func (s *memberStore) removeEmailIndex(ctx context.Context, email, memberID string) error {
	return deleteIndexEntry(ctx, s.db, LookupEmailEntryPath(email, s.accountID, memberID))
}

func (s *memberStore) AddExternalIndex(ctx context.Context, provider, subject, memberID string) error {
	return s.db.Put(ctx, LookupExternalEntryPath(provider, subject, s.accountID, memberID), nowBytes())
}

// RemoveExternalIndex returns ErrNotFound when the entry isn't present —
// KVDB.Delete is silently idempotent, but callers want a not-found signal.
func (s *memberStore) RemoveExternalIndex(ctx context.Context, provider, subject, memberID string) error {
	return deleteIndexEntry(ctx, s.db, LookupExternalEntryPath(provider, subject, s.accountID, memberID))
}

// deleteIndexEntry reads-then-deletes so missing-key surfaces as ErrNotFound;
// KVDB.Delete alone returns nil for missing keys.
func deleteIndexEntry(ctx context.Context, db kvdb.KVDB, key string) error {
	if _, err := db.Get(ctx, key); err != nil {
		if isMissing(err) {
			return ErrNotFound
		}
		return fmt.Errorf("accounts: read index %s: %w", key, err)
	}
	return db.Delete(ctx, key)
}

func readMemberIndexByPrefix(ctx context.Context, db kvdb.KVDB, prefix string) ([]memberIndexEntry, error) {
	keys, err := db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("accounts: list index %s: %w", prefix, err)
	}
	out := make([]memberIndexEntry, 0, len(keys))
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		out = append(out, memberIndexEntry{AccountID: parts[0], MemberID: parts[1]})
	}
	return out, nil
}

func nowBytes() []byte {
	ts := time.Now().UnixNano()
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(ts & 0xff)
		ts >>= 8
	}
	return b
}
