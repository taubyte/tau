package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taubyte/tau/core/kvdb"
	accountsIface "github.com/taubyte/tau/core/services/accounts"
	protocolCommon "github.com/taubyte/tau/services/common"
)

type accountStore struct {
	db kvdb.KVDB
}

func newAccountStore(db kvdb.KVDB) *accountStore { return &accountStore{db: db} }

var _ accountsIface.Accounts = (*accountStore)(nil)

func (s *accountStore) Create(ctx context.Context, in accountsIface.CreateAccountInput) (*accountsIface.Account, error) {
	if err := validateAccountSlug(in.Slug); err != nil {
		return nil, err
	}
	if in.Name == "" {
		return nil, errors.New("accounts: name required")
	}
	if in.Kind == "" {
		in.Kind = accountsIface.AccountKindOrg
	}
	if in.AuthMode == "" {
		in.AuthMode = accountsIface.AuthModeManaged
	}

	if existing, _ := s.lookupIDBySlug(ctx, in.Slug); existing != "" {
		return nil, fmt.Errorf("accounts: slug %q already in use", in.Slug)
	}

	now := time.Now().UTC()
	acc := &accountsIface.Account{
		ID:         protocolCommon.GetNewAccountID(in.Slug, in.Kind, now.UnixNano()),
		Slug:       in.Slug,
		Name:       in.Name,
		Kind:       in.Kind,
		Status:     accountsIface.AccountStatusActive,
		AuthMode:   in.AuthMode,
		AuthConfig: in.AuthConfig,
		Metadata:   in.Metadata,
		CreatedAt:  now,
	}

	if err := putKV(ctx, s.db, AccountProfilePath(acc.ID), acc); err != nil {
		return nil, err
	}
	if err := s.db.Put(ctx, LookupAccountSlugPath(in.Slug), []byte(acc.ID)); err != nil {
		_ = s.db.Delete(ctx, AccountProfilePath(acc.ID))
		return nil, fmt.Errorf("accounts: index slug: %w", err)
	}
	return acc, nil
}

func (s *accountStore) Get(ctx context.Context, accountID string) (*accountsIface.Account, error) {
	var acc accountsIface.Account
	if err := getKV(ctx, s.db, AccountProfilePath(accountID), &acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func (s *accountStore) GetBySlug(ctx context.Context, slug string) (*accountsIface.Account, error) {
	id, err := s.lookupIDBySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrNotFound
	}
	return s.Get(ctx, id)
}

func (s *accountStore) List(ctx context.Context) ([]string, error) {
	return listChildIDs(ctx, s.db, prefixAccounts)
}

// Update is partial — slug and ID are immutable.
func (s *accountStore) Update(ctx context.Context, accountID string, in accountsIface.UpdateAccountInput) (*accountsIface.Account, error) {
	acc, err := s.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		acc.Name = *in.Name
	}
	if in.AuthMode != nil {
		acc.AuthMode = *in.AuthMode
	}
	if in.AuthConfig != nil {
		acc.AuthConfig = in.AuthConfig
	}
	if in.Status != nil {
		acc.Status = *in.Status
	}
	if in.Metadata != nil {
		// Merge, not replace — "" values are kept; explicit removal TBD.
		if acc.Metadata == nil {
			acc.Metadata = map[string]string{}
		}
		for k, v := range in.Metadata {
			acc.Metadata[k] = v
		}
	}
	if err := putKV(ctx, s.db, AccountProfilePath(accountID), acc); err != nil {
		return nil, err
	}
	return acc, nil
}

// Delete does not cascade into Members/Users/PRefs — callers must empty
// those first. Explicit emptying is safer than implicit cascade here.
func (s *accountStore) Delete(ctx context.Context, accountID string) error {
	acc, err := s.Get(ctx, accountID)
	if err != nil {
		return err
	}
	batch, err := s.db.Batch(ctx)
	if err != nil {
		return fmt.Errorf("accounts: batch: %w", err)
	}
	if err := batch.Delete(AccountProfilePath(accountID)); err != nil {
		return fmt.Errorf("accounts: batch delete profile: %w", err)
	}
	if err := batch.Delete(LookupAccountSlugPath(acc.Slug)); err != nil {
		return fmt.Errorf("accounts: batch delete slug index: %w", err)
	}
	if err := batch.Commit(); err != nil {
		return fmt.Errorf("accounts: commit delete: %w", err)
	}
	return nil
}

// lookupIDBySlug returns "" (not ErrNotFound) when the slug is missing.
func (s *accountStore) lookupIDBySlug(ctx context.Context, slug string) (string, error) {
	raw, err := s.db.Get(ctx, LookupAccountSlugPath(slug))
	if err != nil {
		if isMissing(err) {
			return "", nil
		}
		return "", fmt.Errorf("accounts: lookup slug: %w", err)
	}
	return string(raw), nil
}

// validateAccountSlug is case-sensitive — "Pro" and "pro" are distinct.
func validateAccountSlug(slug string) error {
	if slug == "" {
		return errors.New("accounts: slug required")
	}
	if len(slug) > 64 {
		return errors.New("accounts: slug too long (>64)")
	}
	if !isVarnameStart(rune(slug[0])) {
		return fmt.Errorf("accounts: slug must start with a letter or underscore (got %q)", string(slug[0]))
	}
	for _, r := range slug[1:] {
		if !isVarnameRune(r) {
			return fmt.Errorf("accounts: slug must be varname (a-zA-Z0-9_); bad char %q", string(r))
		}
	}
	return nil
}
