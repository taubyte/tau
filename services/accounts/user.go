package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

// userStore implements accountsIface.Users for one Account.
//
// Persistence is split: the User's profile (sans grants) lives at
// .../users/{user_id}/profile, and each PlanGrant lives at
// .../users/{user_id}/grants/{plan_id}. This lets Grant/Revoke modify a
// single grant without touching the profile, and lets List(grants) walk a
// per-User prefix.
//
// Maintained indexes:
//
//	/lookup/git_user/{provider}/{external_id} → JSON [(account_id, user_id), ...]
type userStore struct {
	db        kvdb.KVDB
	accountID string
}

func newUserStore(db kvdb.KVDB, accountID string) *userStore {
	return &userStore{db: db, accountID: accountID}
}

var _ accountsIface.Users = (*userStore)(nil)

// gitUserIndexEntry is one row in the git_user lookup index.
type gitUserIndexEntry struct {
	AccountID string `cbor:"account_id"`
	UserID    string `cbor:"user_id"`
}

// Add records a new linked git account on this Account. Provider+external_id
// must be unique within the Account (a given github user can only be linked
// once per Account). Across Accounts the same git user can be linked many
// times — the global git_user index tracks all of them.
func (s *userStore) Add(ctx context.Context, in accountsIface.AddUserInput) (*accountsIface.User, error) {
	if in.Provider == "" || in.ExternalID == "" {
		return nil, errors.New("accounts: provider and external_id required")
	}
	// Uniqueness within Account.
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
		// best-effort rollback
		_ = s.db.Delete(ctx, UserProfilePath(s.accountID, u.ID))
		return nil, err
	}
	return u, nil
}

// Get loads a User (including its grants).
func (s *userStore) Get(ctx context.Context, userID string) (*accountsIface.User, error) {
	var u accountsIface.User
	if err := getKV(ctx, s.db, UserProfilePath(s.accountID, userID), &u); err != nil {
		return nil, err
	}
	grants, err := s.listGrants(ctx, userID)
	if err != nil {
		return nil, err
	}
	u.PlanGrants = grants
	return &u, nil
}

// GetByExternal finds a User by (provider, external_id) within this Account.
// Walks the global git_user index then filters to this Account.
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

// List returns the IDs of all Users on this Account.
func (s *userStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, AccountUsersPrefix(s.accountID))
}

// Remove deletes the User profile, all grants, and removes the git_user index entry.
func (s *userStore) Remove(ctx context.Context, userID string) error {
	u, err := s.Get(ctx, userID)
	if err != nil {
		return err
	}
	// Delete each grant individually (KVDB lacks a "delete prefix" primitive).
	for _, g := range u.PlanGrants {
		if err := s.db.Delete(ctx, UserGrantPath(s.accountID, userID, g.PlanID)); err != nil {
			return fmt.Errorf("accounts: delete grant: %w", err)
		}
	}
	if err := s.db.Delete(ctx, UserProfilePath(s.accountID, userID)); err != nil {
		return fmt.Errorf("accounts: delete user: %w", err)
	}
	if err := s.removeGitUserIndex(ctx, u.Provider, u.ExternalID, userID); err != nil {
		return err
	}
	return nil
}

// Grant attaches a Plan to the User. If is_default is set, demotes any
// existing default grant. First grant on a User is auto-default.
func (s *userStore) Grant(ctx context.Context, userID string, in accountsIface.GrantPlanInput) error {
	if in.PlanID == "" {
		return errors.New("accounts: plan_id required")
	}
	// Verify the User exists.
	if _, err := s.Get(ctx, userID); err != nil {
		return err
	}
	// Verify the Plan exists in this Account.
	bs := newPlanStore(s.db, s.accountID)
	if _, err := bs.Get(ctx, in.PlanID); err != nil {
		return fmt.Errorf("accounts: plan %s: %w", in.PlanID, err)
	}

	existing, err := s.listGrants(ctx, userID)
	if err != nil {
		return err
	}

	makeDefault := in.IsDefault || len(existing) == 0

	// If this grant is becoming default, demote others.
	if makeDefault {
		for _, g := range existing {
			if g.IsDefault && g.PlanID != in.PlanID {
				demoted := g
				demoted.IsDefault = false
				if err := putKV(ctx, s.db, UserGrantPath(s.accountID, userID, demoted.PlanID), demoted); err != nil {
					return err
				}
			}
		}
	}

	g := accountsIface.PlanGrant{PlanID: in.PlanID, IsDefault: makeDefault}
	if err := putKV(ctx, s.db, UserGrantPath(s.accountID, userID, in.PlanID), g); err != nil {
		return err
	}
	return nil
}

// Revoke removes one grant. If it was the default and other grants remain,
// promotes the first remaining to default.
func (s *userStore) Revoke(ctx context.Context, userID, planID string) error {
	existing, err := s.listGrants(ctx, userID)
	if err != nil {
		return err
	}
	var (
		removed accountsIface.PlanGrant
		found   bool
		others  []accountsIface.PlanGrant
	)
	for _, g := range existing {
		if g.PlanID == planID {
			removed = g
			found = true
		} else {
			others = append(others, g)
		}
	}
	if !found {
		return ErrNotFound
	}
	if err := s.db.Delete(ctx, UserGrantPath(s.accountID, userID, planID)); err != nil {
		return fmt.Errorf("accounts: delete grant: %w", err)
	}
	// Promote the first remaining grant if we removed the default.
	if removed.IsDefault && len(others) > 0 {
		others[0].IsDefault = true
		if err := putKV(ctx, s.db, UserGrantPath(s.accountID, userID, others[0].PlanID), others[0]); err != nil {
			return err
		}
	}
	return nil
}

// listGrants reads all grants for a User.
func (s *userStore) listGrants(ctx context.Context, userID string) ([]accountsIface.PlanGrant, error) {
	keys, err := s.db.List(ctx, UserGrantsPrefix(s.accountID, userID))
	if err != nil {
		return nil, fmt.Errorf("accounts: list grants: %w", err)
	}
	grants := make([]accountsIface.PlanGrant, 0, len(keys))
	for _, k := range keys {
		var g accountsIface.PlanGrant
		if err := getKV(ctx, s.db, k, &g); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, nil
}

// --- git_user index helpers ----------------------------------------

func (s *userStore) addGitUserIndex(ctx context.Context, provider, externalID, userID string) error {
	idx, err := s.readGitUserIndex(ctx, provider, externalID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	idx = append(idx, gitUserIndexEntry{AccountID: s.accountID, UserID: userID})
	return s.writeGitUserIndex(ctx, provider, externalID, idx)
}

func (s *userStore) removeGitUserIndex(ctx context.Context, provider, externalID, userID string) error {
	idx, err := s.readGitUserIndex(ctx, provider, externalID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	out := idx[:0]
	for _, e := range idx {
		if e.AccountID == s.accountID && e.UserID == userID {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		return s.db.Delete(ctx, LookupGitUserPath(provider, externalID))
	}
	return s.writeGitUserIndex(ctx, provider, externalID, out)
}

func (s *userStore) readGitUserIndex(ctx context.Context, provider, externalID string) ([]gitUserIndexEntry, error) {
	raw, err := s.db.Get(ctx, LookupGitUserPath(provider, externalID))
	if err != nil {
		if isMissing(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("accounts: git_user index get: %w", err)
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	var idx []gitUserIndexEntry
	if err := cbor.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("accounts: git_user index unmarshal: %w", err)
	}
	return idx, nil
}

func (s *userStore) writeGitUserIndex(ctx context.Context, provider, externalID string, idx []gitUserIndexEntry) error {
	raw, err := cbor.Marshal(idx)
	if err != nil {
		return fmt.Errorf("accounts: git_user index marshal: %w", err)
	}
	return s.db.Put(ctx, LookupGitUserPath(provider, externalID), raw)
}
