package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// userStore implements accountsIface.Users for one Account — the linked git
// accounts. A linked user is the access grant.
type userStore struct {
	db        kvdb.KVDB
	accountID string
}

func newUserStore(db kvdb.KVDB, accountID string) *userStore {
	return &userStore{db: db, accountID: accountID}
}

var _ accountsIface.Users = (*userStore)(nil)

// gitUserIndexEntry is reconstructed from index keys; not a stored shape.
type gitUserIndexEntry struct {
	AccountID string
	UserID    string
}

// Add enforces (provider, external_id) uniqueness within this Account. The
// same git user may be linked to many Accounts — the global git_user index
// tracks all of them.
func (s *userStore) Add(ctx context.Context, in accountsIface.AddUserInput) (*accountsIface.User, error) {
	if in.Provider == "" || in.ExternalID == "" {
		return nil, errors.New("accounts: provider and external_id required")
	}
	if existing, _ := s.GetByExternal(ctx, in.Provider, in.ExternalID); existing != nil {
		return nil, fmt.Errorf("accounts: git user %s/%s already linked to this account",
			in.Provider, in.ExternalID)
	}
	now := time.Now().UTC()
	u := &accountsIface.User{
		ID:          protocolCommon.GetNewUserID(s.accountID, in.Provider, in.ExternalID, now.UnixNano()),
		AccountID:   s.accountID,
		Provider:    in.Provider,
		ExternalID:  in.ExternalID,
		DisplayName: in.DisplayName,
		AddedAt:     now,
	}
	if err := putKV(ctx, s.db, UserProfilePath(s.accountID, u.ID), u); err != nil {
		return nil, err
	}
	if err := s.addGitUserIndex(ctx, u.Provider, u.ExternalID, u.ID); err != nil {
		_ = s.db.Delete(ctx, UserProfilePath(s.accountID, u.ID))
		return nil, err
	}
	return u, nil
}

func (s *userStore) Get(ctx context.Context, userID string) (*accountsIface.User, error) {
	var u accountsIface.User
	if err := getKV(ctx, s.db, UserProfilePath(s.accountID, userID), &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *userStore) GetByExternal(ctx context.Context, provider, externalID string) (*accountsIface.User, error) {
	idx, err := s.readGitUserIndex(ctx, provider, externalID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	for _, e := range idx {
		if e.AccountID == s.accountID {
			return s.Get(ctx, e.UserID)
		}
	}
	return nil, ErrNotFound
}

func (s *userStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, AccountUsersPrefix(s.accountID))
}

func (s *userStore) Remove(ctx context.Context, userID string) error {
	u, err := s.Get(ctx, userID)
	if err != nil {
		return err
	}
	// ponytail: any extra per-user data keyed under this user's id is left
	// dangling — never read again (a re-linked user gets a fresh id), so no
	// cleanup is needed here.
	if err := s.db.Delete(ctx, UserProfilePath(s.accountID, userID)); err != nil {
		return fmt.Errorf("accounts: delete user: %w", err)
	}
	// git_user-index cleanup is best-effort: profile delete already
	// succeeded, so don't fail Remove because a secondary index is stale.
	if err := s.removeGitUserIndex(ctx, u.Provider, u.ExternalID, userID); err != nil {
		logger.Warnf("accounts: stale git_user index entry for user %s on account %s: %v",
			userID, s.accountID, err)
	}
	return nil
}

func (s *userStore) addGitUserIndex(ctx context.Context, provider, externalID, userID string) error {
	return s.db.Put(ctx, LookupGitUserEntryPath(provider, externalID, s.accountID, userID), nowBytes())
}

// removeGitUserIndex returns ErrNotFound when the entry isn't present (raw
// KVDB.Delete is idempotent).
func (s *userStore) removeGitUserIndex(ctx context.Context, provider, externalID, userID string) error {
	return deleteIndexEntry(ctx, s.db, LookupGitUserEntryPath(provider, externalID, s.accountID, userID))
}

func (s *userStore) readGitUserIndex(ctx context.Context, provider, externalID string) ([]gitUserIndexEntry, error) {
	prefix := LookupGitUserPrefix(provider, externalID)
	keys, err := s.db.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("accounts: list git_user index %s: %w", prefix, err)
	}
	if len(keys) == 0 {
		return nil, ErrNotFound
	}
	out := make([]gitUserIndexEntry, 0, len(keys))
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		out = append(out, gitUserIndexEntry{AccountID: parts[0], UserID: parts[1]})
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}
